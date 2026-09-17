package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fil-forge/libforge/attestation/didmailto"
	blobcmds "github.com/fil-forge/libforge/commands/blob"
	httpcmds "github.com/fil-forge/libforge/commands/http"
	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/did/key"
	"github.com/fil-forge/ucantone/did/resolver"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/command"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/promise"
	"github.com/fil-forge/ucantone/ucan/receipt"
	"github.com/fil-forge/ucantone/validator"
	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// assertLocationCommand is the location commitment a node publishes inside
// an acceptance; the conclude response must carry it back with the receipt.
var assertLocationCommand = command.MustParse("/assert/location")

// countingPiri is a mock storage node that answers /blob/accept per digest and
// counts the HTTP requests it received, which is what batching is meant to
// reduce.
type countingPiri struct {
	url      *url.URL
	requests atomic.Int64
	accepts  atomic.Int64
	// reject fails the accept of any digest whose string form is a key.
	reject map[string]bool

	// acceptTasks records the task link of every accept executed, so a test
	// can look the stored receipts up the way a polling deliverer would.
	mu          sync.Mutex
	acceptTasks map[string]cid.Cid
}

// acceptTask returns the /blob/accept task link recorded for a digest.
func (p *countingPiri) acceptTask(t *testing.T, digest multihash.Multihash) cid.Cid {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	task, ok := p.acceptTasks[string(digest)]
	require.True(t, ok, "no accept recorded for %x", digest)
	return task
}

func newCountingPiri(t *testing.T, storageProvider ucan.Issuer, uploadService identity.Identity) *countingPiri {
	t.Helper()
	p := &countingPiri{reject: map[string]bool{}, acceptTasks: map[string]cid.Cid{}}

	srv := server.NewHTTP(
		storageProvider,
		server.WithValidationOptions(validator.WithDIDResolver(resolver.Tiered{
			resolver.WellKnown{uploadService.DID(): testutil.Must(uploadService.DIDDocument())(t)},
			key.Resolver,
		})),
	)
	srv.Handle(blobcmds.Accept.Command, blobcmds.Accept.Handler(func(
		req *binding.Request[*blobcmds.AcceptArguments],
		res *binding.Response[*blobcmds.AcceptOK],
	) error {
		p.accepts.Add(1)
		args := req.Task().Arguments()
		p.mu.Lock()
		p.acceptTasks[string(args.Blob.Digest)] = req.Task().Link()
		p.mu.Unlock()
		if p.reject[string(args.Blob.Digest)] {
			return res.SetFailure(errors.New("BlobNotFound", "blob not delivered"))
		}
		// A real node attaches its location commitment to the receipt; the
		// conclude response must carry those back to the deliverer.
		claim, err := invocation.Invoke(storageProvider, storageProvider.DID(),
			assertLocationCommand, datamodel.Map{"digest": []byte(args.Blob.Digest)})
		if err != nil {
			return err
		}
		if err := res.SetMetadata(container.New(container.WithInvocations(claim))); err != nil {
			return err
		}
		return res.SetSuccess(&blobcmds.AcceptOK{
			Site: claim.Task().Link(),
			PDP:  promise.AwaitOK{Task: claim.Task().Link()},
		})
	}))

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests.Add(1)
		srv.ServeHTTP(w, r)
	}))
	t.Cleanup(httpSrv.Close)
	p.url = testutil.Must(url.Parse(httpSrv.URL))(t)
	return p
}

// parkedBlob is one blob uploaded but not yet accepted: the allocate
// invocation is in the agent store and the put receipt is ready to deliver.
type parkedBlob struct {
	digest multihash.Multihash
	conc   handlers.Conclusion
}

// parkBlobs records an allocation per blob on the given provider and returns
// the conclusions a client would deliver for them.
func parkBlobs(t *testing.T, ctx context.Context, deps *httpPutDeps, uploadService identity.Identity, storageProvider ucan.Issuer, space did.DID, cause cid.Cid, n int) []parkedBlob {
	t.Helper()
	out := make([]parkedBlob, n)
	for i := range out {
		digest := testutil.RandomMultihash(t)
		blob := blobcmds.Blob{Digest: digest, Size: 1024}

		allocInv, err := blobcmds.Allocate.Invoke(
			uploadService,
			storageProvider.DID(),
			&blobcmds.AllocateArguments{Space: space, Blob: blob, Cause: cause},
			invocation.WithAudience(storageProvider.DID()),
		)
		require.NoError(t, err)
		msg := container.New(container.WithInvocations(allocInv))
		require.NoError(t, deps.agentStore.Write(ctx, msg, agent.Index(msg)))

		blobProvider := deriveBlobProvider(t, digest)
		putInv, err := httpcmds.Put.Invoke(
			blobProvider,
			blobProvider.DID(),
			&httpcmds.PutArguments{Body: blob, Destination: promise.AwaitOK{Task: allocInv.Task().Link()}},
			invocation.WithAudience(blobProvider.DID()),
		)
		require.NoError(t, err)
		putRcpt, err := receipt.IssueOK(blobProvider, putInv.Task().Link(), &httpcmds.PutOK{})
		require.NoError(t, err)

		out[i] = parkedBlob{digest: digest, conc: handlers.Conclusion{Invocation: putInv, Receipt: putRcpt}}
	}
	return out
}

// provisionConcludeSpace makes blob_registry.Register succeed for the space.
func provisionConcludeSpace(t *testing.T, ctx context.Context, deps *httpPutDeps, uploadService identity.Identity, space did.DID) {
	t.Helper()
	account := testutil.Must(didmailto.New("alice@example.com"))(t)
	require.NoError(t, deps.consumerStore.Add(ctx, uploadService.DID(), space, account, "sub-1", testutil.RandomCID(t)))
}

// TestHTTPPutConcludeBatch is the batching gate: many delivered put receipts
// must cost one request per storage node, not one per blob, and every blob's
// accept receipt must come back in the conclude response.
func TestHTTPPutConcludeBatch(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()
	uploadService := testutil.WebService

	t.Run("one request per provider", func(t *testing.T) {
		const blobsPerProvider = 8

		spA := testutil.RandomIssuer(t)
		spB := testutil.RandomIssuer(t)
		piriA := newCountingPiri(t, spA, uploadService)
		piriB := newCountingPiri(t, spB, uploadService)

		deps := newHTTPPutDeps(t, piriclient.NewProvider(uploadService, logger), logger)
		require.NoError(t, deps.spStore.Put(ctx, spA.DID(), *piriA.url, 100, nil, providerProofs(t, spA, uploadService)))
		require.NoError(t, deps.spStore.Put(ctx, spB.DID(), *piriB.url, 100, nil, providerProofs(t, spB, uploadService)))

		space := testutil.RandomIssuer(t)
		provisionConcludeSpace(t, ctx, deps, uploadService, space.DID())
		cause := testutil.RandomCID(t)

		parkedA := parkBlobs(t, ctx, deps, uploadService, spA, space.DID(), cause, blobsPerProvider)
		parkedB := parkBlobs(t, ctx, deps, uploadService, spB, space.DID(), cause, blobsPerProvider)

		// Interleave the two providers' blobs: grouping is by allocation, not
		// by delivery order.
		var conclusions []handlers.Conclusion
		for i := 0; i < blobsPerProvider; i++ {
			conclusions = append(conclusions, parkedA[i].conc, parkedB[i].conc)
		}

		meta, err := deps.ch.Handler(ctx, conclusions)
		require.NoError(t, err)

		require.EqualValues(t, 1, piriA.requests.Load(), "provider A should receive one batched request")
		require.EqualValues(t, 1, piriB.requests.Load(), "provider B should receive one batched request")
		require.EqualValues(t, blobsPerProvider, piriA.accepts.Load())
		require.EqualValues(t, blobsPerProvider, piriB.accepts.Load())

		// Every blob is registered and every accept receipt came back.
		for _, p := range append(append([]parkedBlob{}, parkedA...), parkedB...) {
			rec, err := deps.blobReg.Get(ctx, space.DID(), p.digest)
			require.NoError(t, err, "blob %x not registered", p.digest)
			require.Equal(t, cause, rec.Cause)
		}
		require.Len(t, meta.Receipts(), 2*blobsPerProvider)
	})

	t.Run("a failed accept does not sink the batch", func(t *testing.T) {
		const blobs = 4

		sp := testutil.RandomIssuer(t)
		piri := newCountingPiri(t, sp, uploadService)

		deps := newHTTPPutDeps(t, piriclient.NewProvider(uploadService, logger), logger)
		require.NoError(t, deps.spStore.Put(ctx, sp.DID(), *piri.url, 100, nil, providerProofs(t, sp, uploadService)))

		space := testutil.RandomIssuer(t)
		provisionConcludeSpace(t, ctx, deps, uploadService, space.DID())
		cause := testutil.RandomCID(t)
		parked := parkBlobs(t, ctx, deps, uploadService, sp, space.DID(), cause, blobs)

		// The node refuses the second blob.
		doomed := parked[1]
		piri.reject[string(doomed.digest)] = true

		conclusions := make([]handlers.Conclusion, len(parked))
		for i, p := range parked {
			conclusions[i] = p.conc
		}

		// The conclusion itself succeeds: the refusal is reported by that
		// blob's own failure receipt.
		meta, err := deps.ch.Handler(ctx, conclusions)
		require.NoError(t, err)
		require.Len(t, meta.Receipts(), blobs)

		var failures int
		for _, r := range meta.Receipts() {
			if r.Out().IsErr() {
				failures++
			}
		}
		require.Equal(t, 1, failures)

		for i, p := range parked {
			_, err := deps.blobReg.Get(ctx, space.DID(), p.digest)
			if i == 1 {
				require.Error(t, err, "a refused blob must not be registered")
				continue
			}
			require.NoError(t, err, "blob %d should be registered despite its neighbour failing", i)
		}
	})
}

// TestHTTPPutConcludeAcceptsEveryInvocationInABatch pins the two properties a
// batched accept rests on, both of which are easy to assume and hard to spot
// if they regress: the node executes every invocation in one request, not
// just the first, and the response carries a receipt for every one of them —
// including the primary, whose receipt ucantone also surfaces separately.
func TestHTTPPutConcludeAcceptsEveryInvocationInABatch(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()
	uploadService := testutil.WebService

	sp := testutil.RandomIssuer(t)
	piri := newCountingPiri(t, sp, uploadService)
	deps := newHTTPPutDeps(t, piriclient.NewProvider(uploadService, logger), logger)
	require.NoError(t, deps.spStore.Put(ctx, sp.DID(), *piri.url, 100, nil, providerProofs(t, sp, uploadService)))

	space := testutil.RandomIssuer(t)
	provisionConcludeSpace(t, ctx, deps, uploadService, space.DID())
	cause := testutil.RandomCID(t)

	const blobs = 5
	parked := parkBlobs(t, ctx, deps, uploadService, sp, space.DID(), cause, blobs)
	conclusions := make([]handlers.Conclusion, len(parked))
	for i, p := range parked {
		conclusions[i] = p.conc
	}

	meta, err := deps.ch.Handler(ctx, conclusions)
	require.NoError(t, err)

	// One request, every invocation executed.
	require.EqualValues(t, 1, piri.requests.Load())
	require.EqualValues(t, blobs, piri.accepts.Load())

	// A receipt came back for every blob, the first included, and every blob
	// registered. A response that dropped the primary receipt would leave the
	// first blob of each request unregistered.
	require.Len(t, meta.Receipts(), blobs)
	for i, p := range parked {
		_, err := deps.blobReg.Get(ctx, space.DID(), p.digest)
		require.NoError(t, err, "blob %d (of %d) not registered", i, blobs)
	}
}

// TestHTTPPutConcludeSkipsFailedPuts pins that a failed /http/put is never
// accepted: the bytes never landed, so there is nothing for the node to
// accept, and the rest of the batch must still go through.
func TestHTTPPutConcludeSkipsFailedPuts(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()
	uploadService := testutil.WebService

	sp := testutil.RandomIssuer(t)
	piri := newCountingPiri(t, sp, uploadService)

	deps := newHTTPPutDeps(t, piriclient.NewProvider(uploadService, logger), logger)
	require.NoError(t, deps.spStore.Put(ctx, sp.DID(), *piri.url, 100, nil, providerProofs(t, sp, uploadService)))

	space := testutil.RandomIssuer(t)
	provisionConcludeSpace(t, ctx, deps, uploadService, space.DID())
	cause := testutil.RandomCID(t)
	parked := parkBlobs(t, ctx, deps, uploadService, sp, space.DID(), cause, 3)

	// The middle blob's client reports that its upload failed.
	conclusions := make([]handlers.Conclusion, len(parked))
	for i, p := range parked {
		conclusions[i] = p.conc
	}
	failed := parked[1]
	blobProvider := deriveBlobProvider(t, failed.digest)
	failedRcpt, err := receipt.IssueErr(blobProvider, failed.conc.Invocation.Task().Link(), datamodel.Map{
		"name":    "UploadFailed",
		"message": "the bytes never arrived",
	})
	require.NoError(t, err)
	conclusions[1] = handlers.Conclusion{Invocation: failed.conc.Invocation, Receipt: failedRcpt}

	_, err = deps.ch.Handler(ctx, conclusions)
	require.NoError(t, err)

	// Only the two successful puts reached the node.
	require.EqualValues(t, 2, piri.accepts.Load(), "a failed put must not be accepted")

	_, err = deps.blobReg.Get(ctx, space.DID(), failed.digest)
	require.Error(t, err, "a failed put must not register its blob")
	for i, p := range parked {
		if i == 1 {
			continue
		}
		_, err := deps.blobReg.Get(ctx, space.DID(), p.digest)
		require.NoError(t, err, "blob %d should still be registered", i)
	}
}

// TestHTTPPutConcludeAnswersLargeBatchByPolling pins what happens when a
// conclusion is too large to answer in one container: every put is still
// accepted and registered, and the response carries nothing rather than a
// truncated set, leaving the deliverer to poll. Anything that decoded is
// processed — the wire format's own limit is the only ceiling.
func TestHTTPPutConcludeAnswersLargeBatchByPolling(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()
	uploadService := testutil.WebService

	sp := testutil.RandomIssuer(t)
	piri := newCountingPiri(t, sp, uploadService)
	deps := newHTTPPutDeps(t, piriclient.NewProvider(uploadService, logger), logger)
	require.NoError(t, deps.spStore.Put(ctx, sp.DID(), *piri.url, 100, nil, providerProofs(t, sp, uploadService)))

	space := testutil.RandomIssuer(t)
	provisionConcludeSpace(t, ctx, deps, uploadService, space.DID())
	cause := testutil.RandomCID(t)

	// Reaching the real budget takes a couple of thousand blobs, so the budget
	// is shrunk instead: the paths under test are the same, and the test stays
	// fast enough for the unit suite. One token is below anything a non-empty
	// conclusion can produce, so the assertions do not depend on how many
	// artifacts the mock node happens to attach per accept.
	const blobs = 8
	handlers.SetContainerTokenBudget(t, 1)
	parked := parkBlobs(t, ctx, deps, uploadService, sp, space.DID(), cause, blobs)
	conclusions := make([]handlers.Conclusion, len(parked))
	for i, p := range parked {
		conclusions[i] = p.conc
	}

	meta, err := deps.ch.Handler(ctx, conclusions)
	require.NoError(t, err)
	require.Nil(t, meta, "an unanswerable conclusion returns no container, not a truncated one")

	// The work still happened: every blob accepted and registered.
	require.EqualValues(t, blobs, piri.accepts.Load())
	for i, p := range parked {
		_, err := deps.blobReg.Get(ctx, space.DID(), p.digest)
		require.NoError(t, err, "blob %d of %d not registered", i, blobs)
	}

	// And every accept receipt is retrievable by task link, which is what the
	// deliverer polls for. One agent message could not have held them all, so
	// this is what proves persistence chunked rather than failed.
	for _, p := range []parkedBlob{parked[0], parked[blobs/2], parked[blobs-1]} {
		_, err := deps.agentStore.GetReceipt(ctx, piri.acceptTask(t, p.digest))
		require.NoError(t, err, "accept receipt for %x not retrievable", p.digest)
	}
}
