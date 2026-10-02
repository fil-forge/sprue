package handlers

import (
	"bytes"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/fil-forge/libforge/attestation/didmailto"
	"github.com/fil-forge/libforge/commands"
	assertcmds "github.com/fil-forge/libforge/commands/assert"
	blobcmds "github.com/fil-forge/libforge/commands/blob"
	httpcmds "github.com/fil-forge/libforge/commands/http"
	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/store/agent"
	agent_store "github.com/fil-forge/sprue/pkg/store/agent/memory"
	blob_registry "github.com/fil-forge/sprue/pkg/store/blob_registry/memory"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	metrics_store "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	routing_policy_store "github.com/fil-forge/sprue/pkg/store/routing_policy/memory"
	spacediff_store "github.com/fil-forge/sprue/pkg/store/space_diff/memory"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	subscription_store "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/did/key"
	"github.com/fil-forge/ucantone/did/resolver"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/delegation"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/promise"
	"github.com/fil-forge/ucantone/ucan/receipt"
	"github.com/fil-forge/ucantone/validator"
	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// A /blob/add can name only the hash function and the size: the client
// streams the data and reports its digest in the /http/put receipt. These
// tests drive that path from add through conclude and abort against a node
// that behaves as piri does.

// digestlessAccept is one /blob/accept the node received.
type digestlessAccept struct {
	task       cid.Cid
	args       blobcmds.AcceptArguments
	putInv     bool
	putReceipt ucan.Receipt
}

// digestlessPiri is a storage node that allocates without a digest and, on
// accept, takes the digest from the /http/put receipt in the request.
type digestlessPiri struct {
	provider ucan.Issuer
	url      *url.URL

	// unsupported makes every allocation fail with UnsupportedDigestCode.
	unsupported bool
	// claimContent, when set, is the content the location commitment names in
	// place of the digest the put receipt reports.
	claimContent multihash.Multihash

	mu        sync.Mutex
	allocates []blobcmds.AllocateArguments
	accepts   []digestlessAccept
	rejects   []blobcmds.RejectArguments
}

func newDigestlessPiri(t *testing.T, uploadService identity.Identity) *digestlessPiri {
	t.Helper()
	p := &digestlessPiri{provider: testutil.RandomIssuer(t)}
	srv := server.NewHTTP(
		p.provider,
		server.WithValidationOptions(validator.WithDIDResolver(resolver.Tiered{
			resolver.WellKnown{uploadService.DID(): testutil.Must(uploadService.DIDDocument())(t)},
			key.Resolver,
		})),
	)
	putURL := testutil.Must(url.Parse("https://storage.example.com/put"))(t)

	srv.Handle(blobcmds.Allocate.Command, blobcmds.Allocate.Handler(func(
		req *binding.Request[*blobcmds.AllocateArguments],
		res *binding.Response[*blobcmds.AllocateOK],
	) error {
		args := req.Task().Arguments()
		p.mu.Lock()
		p.allocates = append(p.allocates, *args)
		p.mu.Unlock()
		if p.unsupported {
			return res.SetFailure(blobcmds.ErrUnsupportedDigestCode)
		}
		return res.SetSuccess(&blobcmds.AllocateOK{
			Size:    args.Blob.Size(),
			Address: &blobcmds.BlobAddress{URL: commands.CborURL(*putURL), Headers: map[string]string{}},
		})
	}))

	srv.Handle(blobcmds.Accept.Command, blobcmds.Accept.Handler(func(
		req *binding.Request[*blobcmds.AcceptArguments],
		res *binding.Response[*blobcmds.AcceptOK],
	) error {
		args := req.Task().Arguments()
		acc := digestlessAccept{task: req.Task().Link(), args: *args}
		for _, inv := range req.Metadata().Invocations() {
			if inv.Task().Link() == args.Put.Task {
				acc.putInv = true
			}
		}
		acc.putReceipt, _ = req.Metadata().Receipt(args.Put.Task)
		p.mu.Lock()
		p.accepts = append(p.accepts, acc)
		p.mu.Unlock()

		if acc.putReceipt == nil {
			return res.SetFailure(errors.New("MissingPutReceipt", "no %s receipt in the request", httpcmds.Put.Command))
		}
		content, err := putDigest(acc.putReceipt, args.Blob.DigestCode())
		if err != nil {
			return err
		}
		if p.claimContent != nil {
			content = p.claimContent
		}
		claim, err := assertcmds.Location.Invoke(p.provider, p.provider.DID(), &assertcmds.LocationArguments{
			Space:    args.Space,
			Content:  content,
			Location: []commands.CborURL{commands.CborURL(*putURL)},
		})
		if err != nil {
			return err
		}
		if err := res.SetMetadata(container.New(container.WithInvocations(claim))); err != nil {
			return err
		}
		return res.SetSuccess(&blobcmds.AcceptOK{
			Site: claim.Link(),
			PDP:  promise.AwaitOK{Task: testutil.RandomCID(t)},
		})
	}))

	srv.Handle(blobcmds.Reject.Command, blobcmds.Reject.Handler(func(
		req *binding.Request[*blobcmds.RejectArguments],
		res *binding.Response[*blobcmds.RejectOK],
	) error {
		p.mu.Lock()
		p.rejects = append(p.rejects, *req.Task().Arguments())
		p.mu.Unlock()
		return res.SetSuccess(&blobcmds.RejectOK{})
	}))

	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)
	p.url = testutil.Must(url.Parse(httpSrv.URL))(t)
	return p
}

func (p *digestlessPiri) Allocates() []blobcmds.AllocateArguments {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]blobcmds.AllocateArguments(nil), p.allocates...)
}

func (p *digestlessPiri) Accepts() []digestlessAccept {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]digestlessAccept(nil), p.accepts...)
}

func (p *digestlessPiri) Rejects() []blobcmds.RejectArguments {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]blobcmds.RejectArguments(nil), p.rejects...)
}

type digestlessWorld struct {
	uploadService identity.Identity
	piri          *digestlessPiri
	space         ucan.Issuer
	agentStore    *agent_store.Store
	blobReg       *blob_registry.Store
	add           server.Route
	abort         server.Route
	conclude      ConclusionHandler
}

func newDigestlessWorld(t *testing.T) *digestlessWorld {
	t.Helper()
	logger := zaptest.NewLogger(t)
	uploadService := testutil.WebService
	w := &digestlessWorld{
		uploadService: uploadService,
		piri:          newDigestlessPiri(t, uploadService),
		space:         testutil.RandomIssuer(t),
		agentStore:    agent_store.New(),
		blobReg: blob_registry.New(
			spacediff_store.New(),
			metrics_store.NewSpaceStore(),
			metrics_store.New(),
		),
	}

	consumerStore := consumer_store.New()
	account := testutil.Must(didmailto.New("alice@example.com"))(t)
	require.NoError(t, consumerStore.Add(t.Context(), uploadService.DID(), w.space.DID(), account, "sub-1", testutil.RandomCID(t)))
	provisioningSvc := provisioning.NewService([]did.DID{uploadService.DID()}, consumerStore, subscription_store.New())

	spStore := storage_provider_store.New()
	provider := w.piri.provider
	var proofs []ucan.Delegation
	for _, cmd := range []ucan.Command{blobcmds.Allocate.Command, blobcmds.Accept.Command, blobcmds.Reject.Command} {
		proofs = append(proofs, testutil.Must(delegation.Delegate(
			provider, uploadService.DID(), provider.DID(), cmd, delegation.WithNoExpiration()))(t))
	}
	require.NoError(t, spStore.Put(t.Context(), provider.DID(), *w.piri.url, 100, nil,
		container.New(container.WithDelegations(proofs...))))

	router := routing.NewService(spStore, routing_policy_store.New(), logger)
	nodeProvider := piriclient.NewProvider(uploadService, logger)
	w.add = NewBlobAddHandler(uploadService, provisioningSvc, router, nodeProvider, w.agentStore, w.blobReg, logger)
	w.abort = NewBlobAbortHandler(router, nodeProvider, w.agentStore, logger)
	w.conclude = NewHTTPPutConcludeHandler(router, nodeProvider, w.agentStore, w.blobReg, logger)
	return w
}

// addResult is a digest-less /blob/add's receipt and the invocations its
// response carried.
type addResult struct {
	inv      ucan.Invocation
	receipt  ucan.Receipt
	allocInv ucan.Invocation
	putInv   ucan.Invocation
	accInv   ucan.Invocation
}

// addDigestless sends a /blob/add naming only SHA2-256 and size, and persists
// the response as the server does.
func (w *digestlessWorld) addDigestless(t *testing.T, size uint64, opts ...invocation.Option) addResult {
	t.Helper()
	opts = append(opts, invocation.WithAudience(w.uploadService.DID()))
	inv := testutil.Must(blobcmds.Add.Invoke(
		testutil.Alice,
		w.space.DID(),
		&blobcmds.AddArguments{Blob: blobcmds.SpecFromDigestCode(multihash.SHA2_256, size)},
		opts...,
	))(t)
	return w.send(t, inv)
}

// send executes a /blob/add and persists the response as the server does.
func (w *digestlessWorld) send(t *testing.T, inv ucan.Invocation) addResult {
	t.Helper()
	req := execution.NewRequest(t.Context(), inv)
	res := testutil.Must(execution.NewResponse(inv.Task().Link(), execution.WithIssuer(w.uploadService)))(t)
	require.NoError(t, w.add.Handler(req, res))

	out := addResult{inv: inv, receipt: res.Receipt()}
	if res.Receipt().Out().IsErr() {
		return out
	}
	meta := res.Metadata()
	for _, i := range meta.Invocations() {
		switch i.Command() {
		case blobcmds.Allocate.Command:
			out.allocInv = i
		case httpcmds.Put.Command:
			out.putInv = i
		case blobcmds.Accept.Command:
			out.accInv = i
		}
	}
	msg := container.New(
		container.WithInvocations(append([]ucan.Invocation{inv}, meta.Invocations()...)...),
		container.WithReceipts(append([]ucan.Receipt{res.Receipt()}, meta.Receipts()...)...),
	)
	require.NoError(t, w.agentStore.Write(t.Context(), msg, agent.Index(msg)))
	return out
}

// putReceipt is the receipt the client issues once it has streamed the data,
// signed with the key the put invocation carries.
func putReceipt(t *testing.T, add addResult, ok *httpcmds.PutOK) ucan.Receipt {
	t.Helper()
	signer := testutil.Must(deriveDID(add.inv.Task().Link().Hash()))(t)
	return testutil.Must(receipt.IssueOK(signer, add.putInv.Task().Link(), ok))(t)
}

func failureName(t *testing.T, rcpt ucan.Receipt) string {
	t.Helper()
	_, err := blobcmds.Add.Unpack(rcpt)
	var model datamodel.ErrorModel
	require.ErrorAs(t, err, &model)
	return model.Name()
}

func TestDigestlessBlobAdd(t *testing.T) {
	t.Run("allocates by digest code and defers the accept", func(t *testing.T) {
		w := newDigestlessWorld(t)
		add := w.addDigestless(t, 1024)
		addOK := testutil.Must(blobcmds.Add.Unpack(add.receipt))(t)

		allocs := w.piri.Allocates()
		require.Len(t, allocs, 1)
		_, hasDigest := allocs[0].Blob.Digest()
		require.False(t, hasDigest, "the allocation names no digest")
		require.Equal(t, uint64(multihash.SHA2_256), allocs[0].Blob.DigestCode())
		require.EqualValues(t, 1024, allocs[0].Blob.Size())
		require.Equal(t, add.inv.Task().Link(), allocs[0].Cause)

		key := testutil.Must(deriveDID(add.inv.Task().Link().Hash()))(t)
		require.Equal(t, key.DID(), add.putInv.Issuer(), "the put key derives from the add task")
		var putArgs httpcmds.PutArguments
		require.NoError(t, putArgs.UnmarshalCBOR(bytes.NewReader(add.putInv.ArgumentsBytes())))
		_, hasDigest = putArgs.Body.Digest()
		require.False(t, hasDigest, "the put body names no digest")

		var accArgs blobcmds.AcceptArguments
		require.NoError(t, accArgs.UnmarshalCBOR(bytes.NewReader(add.accInv.ArgumentsBytes())))
		_, hasDigest = accArgs.Blob.Digest()
		require.False(t, hasDigest, "the accept names no digest")
		require.Equal(t, add.accInv.Task().Link(), addOK.Site.Task)
		require.Empty(t, w.piri.Accepts(), "nothing is accepted before the put")
	})

	t.Run("requires a nonce", func(t *testing.T) {
		w := newDigestlessWorld(t)
		add := w.addDigestless(t, 1024, invocation.WithNoNonce())
		require.Equal(t, MissingNonceErrorName, failureName(t, add.receipt))
		require.Empty(t, w.piri.Allocates())
	})

	t.Run("reports an unsupported digest code", func(t *testing.T) {
		w := newDigestlessWorld(t)
		w.piri.unsupported = true
		add := w.addDigestless(t, 1024)
		require.Equal(t, blobcmds.UnsupportedDigestCodeErrorName, failureName(t, add.receipt))
	})
}

func TestDigestlessConclude(t *testing.T) {
	t.Run("accepts with the put evidence and registers the reported digest", func(t *testing.T) {
		w := newDigestlessWorld(t)
		add := w.addDigestless(t, 1024)
		addOK := testutil.Must(blobcmds.Add.Unpack(add.receipt))(t)
		digest := testutil.RandomMultihash(t)
		rcpt := putReceipt(t, add, &httpcmds.PutOK{Blob: &httpcmds.PutBlob{Digest: digest}})

		meta, err := w.conclude.Handler(t.Context(), []Conclusion{{Invocation: add.putInv, Receipt: rcpt}})
		require.NoError(t, err)

		accepts := w.piri.Accepts()
		require.Len(t, accepts, 1)
		require.Equal(t, addOK.Site.Task, accepts[0].task, "the accept is the task /blob/add promised")
		require.True(t, accepts[0].putInv, "the put invocation travels with the accept")
		require.NotNil(t, accepts[0].putReceipt, "the put receipt travels with the accept")
		require.Equal(t, rcpt.Link(), accepts[0].putReceipt.Link())

		_, err = blobcmds.Accept.Unpack(meta.Receipts()[0])
		require.NoError(t, err)
		rec, err := w.blobReg.Get(t.Context(), w.space.DID(), digest)
		require.NoError(t, err, "registered under the digest the put reports")
		require.Equal(t, add.inv.Task().Link(), rec.Cause)
		require.Equal(t, uint64(1024), rec.Blob.Size)
	})

	t.Run("skips a put receipt that reports no digest", func(t *testing.T) {
		w := newDigestlessWorld(t)
		add := w.addDigestless(t, 1024)
		rcpt := putReceipt(t, add, &httpcmds.PutOK{})

		_, err := w.conclude.Handler(t.Context(), []Conclusion{{Invocation: add.putInv, Receipt: rcpt}})
		require.NoError(t, err)
		require.Empty(t, w.piri.Accepts())
	})

	t.Run("skips a reported digest of another hash function", func(t *testing.T) {
		w := newDigestlessWorld(t)
		add := w.addDigestless(t, 1024)
		digest := testutil.Must(multihash.Sum([]byte("data"), multihash.SHA2_512, -1))(t)
		rcpt := putReceipt(t, add, &httpcmds.PutOK{Blob: &httpcmds.PutBlob{Digest: digest}})

		_, err := w.conclude.Handler(t.Context(), []Conclusion{{Invocation: add.putInv, Receipt: rcpt}})
		require.NoError(t, err)
		require.Empty(t, w.piri.Accepts())
	})

	t.Run("does not register when the location commitment names other content", func(t *testing.T) {
		w := newDigestlessWorld(t)
		w.piri.claimContent = testutil.RandomMultihash(t)
		add := w.addDigestless(t, 1024)
		digest := testutil.RandomMultihash(t)
		rcpt := putReceipt(t, add, &httpcmds.PutOK{Blob: &httpcmds.PutBlob{Digest: digest}})

		_, err := w.conclude.Handler(t.Context(), []Conclusion{{Invocation: add.putInv, Receipt: rcpt}})
		require.NoError(t, err)
		require.Len(t, w.piri.Accepts(), 1)
		_, err = w.blobReg.Get(t.Context(), w.space.DID(), digest)
		require.Error(t, err, "nothing is registered")
	})
}

func TestDigestlessAbort(t *testing.T) {
	w := newDigestlessWorld(t)
	add := w.addDigestless(t, 1024)
	abort := func(space ucan.Issuer) ucan.Receipt {
		inv := testutil.Must(blobcmds.Abort.Invoke(
			testutil.Alice,
			space.DID(),
			&blobcmds.AbortArguments{Add: add.inv.Task().Link()},
			invocation.WithAudience(w.uploadService.DID()),
		))(t)
		req := execution.NewRequest(t.Context(), inv)
		res := testutil.Must(execution.NewResponse(inv.Task().Link(), execution.WithIssuer(w.uploadService)))(t)
		require.NoError(t, w.abort.Handler(req, res))
		return res.Receipt()
	}

	t.Run("another space cannot abort the upload", func(t *testing.T) {
		_, err := blobcmds.Abort.Unpack(abort(testutil.RandomIssuer(t)))
		var model datamodel.ErrorModel
		require.ErrorAs(t, err, &model)
		require.Equal(t, blobcmds.MissingCauseErrorName, model.Name())
		require.Empty(t, w.piri.Rejects())
	})

	t.Run("the space rejects the allocation its add made", func(t *testing.T) {
		_, err := blobcmds.Abort.Unpack(abort(w.space))
		require.NoError(t, err)
		rejects := w.piri.Rejects()
		require.Len(t, rejects, 1)
		require.Equal(t, add.allocInv.Task().Link(), rejects[0].Allocation)
	})
}

func TestDigestlessBlobAddReplay(t *testing.T) {
	w := newDigestlessWorld(t)
	first := w.addDigestless(t, 1024)
	firstOK := testutil.Must(blobcmds.Add.Unpack(first.receipt))(t)

	t.Run("before the put is concluded", func(t *testing.T) {
		again := w.send(t, first.inv)
		againOK := testutil.Must(blobcmds.Add.Unpack(again.receipt))(t)
		require.Equal(t, firstOK.Site.Task, againOK.Site.Task, "the replay promises the same accept")
		require.Equal(t, first.allocInv.Task().Link(), again.allocInv.Task().Link())
		require.Equal(t, first.putInv.Task().Link(), again.putInv.Task().Link())
		require.Len(t, w.piri.Allocates(), 1, "nothing is allocated again")
	})

	t.Run("after the put is concluded", func(t *testing.T) {
		digest := testutil.RandomMultihash(t)
		rcpt := putReceipt(t, first, &httpcmds.PutOK{Blob: &httpcmds.PutBlob{Digest: digest}})
		_, err := w.conclude.Handler(t.Context(), []Conclusion{{Invocation: first.putInv, Receipt: rcpt}})
		require.NoError(t, err)
		// The conclude's artifacts are persisted as the server persists the
		// response carrying them.
		msg := container.New(container.WithReceipts(rcpt))
		require.NoError(t, w.agentStore.Write(t.Context(), msg, agent.Index(msg)))

		inv := first.inv
		req := execution.NewRequest(t.Context(), inv)
		res := testutil.Must(execution.NewResponse(inv.Task().Link(), execution.WithIssuer(w.uploadService)))(t)
		require.NoError(t, w.add.Handler(req, res))
		againOK := testutil.Must(blobcmds.Add.Unpack(res.Receipt()))(t)
		require.Equal(t, firstOK.Site.Task, againOK.Site.Task)
		ran := map[cid.Cid]bool{}
		for _, r := range res.Metadata().Receipts() {
			ran[r.Ran()] = true
		}
		require.True(t, ran[first.putInv.Task().Link()], "the put receipt comes back")
		require.True(t, ran[firstOK.Site.Task], "the accept receipt comes back")
		require.Len(t, w.piri.Allocates(), 1)
		require.Len(t, w.piri.Accepts(), 1, "nothing is accepted again")
	})

	t.Run("a failed add replays its failure", func(t *testing.T) {
		w := newDigestlessWorld(t)
		w.piri.unsupported = true
		failed := w.addDigestless(t, 1024)
		require.Equal(t, blobcmds.UnsupportedDigestCodeErrorName, failureName(t, failed.receipt))
		// The server persists the failure receipt with the invocation.
		msg := container.New(container.WithInvocations(failed.inv), container.WithReceipts(failed.receipt))
		require.NoError(t, w.agentStore.Write(t.Context(), msg, agent.Index(msg)))

		w.piri.unsupported = false
		again := w.send(t, failed.inv)
		require.Equal(t, blobcmds.UnsupportedDigestCodeErrorName, failureName(t, again.receipt))
		require.Len(t, w.piri.Allocates(), 1, "the replay does not try again")
	})
}
