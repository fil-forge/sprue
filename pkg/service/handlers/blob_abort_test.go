package handlers_test

import (
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	httpcmds "github.com/fil-forge/libforge/commands/http"
	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	"github.com/fil-forge/sprue/pkg/store/agent"
	agent_store "github.com/fil-forge/sprue/pkg/store/agent/memory"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/did/key"
	"github.com/fil-forge/ucantone/did/resolver"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/multikey"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/promise"
	"github.com/fil-forge/ucantone/ucan/receipt"
	"github.com/fil-forge/ucantone/validator"
	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

type blobAbortTestDeps struct {
	handler    server.Route
	spStore    *storage_provider_store.Store
	agentStore *agent_store.Store
}

func newBlobAbortTestDeps(t *testing.T, uploadService multikey.Issuer, logger *zap.Logger) *blobAbortTestDeps {
	t.Helper()
	spStore := storage_provider_store.New()
	router := routing.NewService(spStore, logger)
	agentStore := agent_store.New()
	nodeProvider := piriclient.NewProvider(uploadService, logger)
	handler := handlers.NewBlobAbortHandler(router, nodeProvider, agentStore, logger)
	return &blobAbortTestDeps{
		handler:    handler,
		spStore:    spStore,
		agentStore: agentStore,
	}
}

// mockPiriRejectServer serves /blob/reject and records each call.
type mockPiriRejectServer struct {
	srv *httptest.Server

	mu    sync.Mutex
	calls []blobcmds.RejectArguments
}

func newMockPiriRejectServer(t *testing.T, storageProvider ucan.Issuer, uploadService identity.Identity) *mockPiriRejectServer {
	t.Helper()
	m := &mockPiriRejectServer{}

	srv := server.NewHTTP(
		storageProvider,
		server.WithValidationOptions(validator.WithDIDResolver(resolver.Tiered{
			resolver.WellKnown{uploadService.DID(): testutil.Must(uploadService.DIDDocument())(t)},
			key.Resolver,
		})),
	)
	srv.Handle(blobcmds.Reject.Command, blobcmds.Reject.Handler(func(
		req *binding.Request[*blobcmds.RejectArguments],
		res *binding.Response[*blobcmds.RejectOK],
	) error {
		m.mu.Lock()
		m.calls = append(m.calls, *req.Task().Arguments())
		m.mu.Unlock()
		return res.SetSuccess(&blobcmds.RejectOK{})
	}))

	m.srv = httptest.NewServer(srv)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockPiriRejectServer) Calls() []blobcmds.RejectArguments {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]blobcmds.RejectArguments(nil), m.calls...)
}

// seedParkedBlobChain persists the receipt chain a PARKED blob leaves behind:
// the /space/blob/add invocation + receipt (whose Site promise points at the
// accept invocation, subject = provider) and the accept/put/alloc
// invocations — but NO accept receipt and NO registry entry, exactly the
// deferred-conclude state. Returns the add task link (the abort Cause).
func seedParkedBlobChain(
	t *testing.T,
	agentStore *agent_store.Store,
	uploadService ucan.Issuer,
	storageProvider ucan.Issuer,
	space did.DID,
	blob blobcmds.Blob,
) cid.Cid {
	t.Helper()
	ctx := t.Context()

	allocInv := testutil.Must(blobcmds.Allocate.Invoke(
		uploadService,
		storageProvider.DID(),
		&blobcmds.AllocateArguments{Space: space, Blob: blob, Cause: testutil.RandomCID(t)},
		invocation.WithAudience(storageProvider.DID()),
	))(t)
	putInv := testutil.Must(httpcmds.Put.Invoke(
		deriveBlobProvider(t, blob.Digest),
		deriveBlobProvider(t, blob.Digest).DID(),
		&httpcmds.PutArguments{
			Body:        blob,
			Destination: promise.AwaitOK{Task: allocInv.Task().Link()},
		},
	))(t)
	accInv := testutil.Must(blobcmds.Accept.Invoke(
		uploadService,
		storageProvider.DID(),
		&blobcmds.AcceptArguments{
			Space: space,
			Blob:  blob,
			Put:   promise.AwaitOK{Task: putInv.Task().Link()},
		},
		invocation.WithAudience(storageProvider.DID()),
	))(t)

	addInv := testutil.Must(blobcmds.Add.Invoke(
		testutil.Alice,
		space,
		&blobcmds.AddArguments{Blob: blob},
		invocation.WithAudience(uploadService.DID()),
	))(t)
	addRcpt := testutil.Must(receipt.IssueOK(
		uploadService,
		addInv.Task().Link(),
		&blobcmds.AddOK{Site: promise.AwaitOK{Task: accInv.Task().Link()}},
	))(t)

	msg := container.New(
		container.WithInvocations(allocInv, putInv, accInv, addInv),
		container.WithReceipts(addRcpt),
	)
	require.NoError(t, agentStore.Write(ctx, msg, agent.Index(msg)))
	return addInv.Task().Link()
}

func invokeBlobAbort(
	t *testing.T,
	deps *blobAbortTestDeps,
	uploadService ucan.Issuer,
	space ucan.Principal,
	digest multihash.Multihash,
	cause cid.Cid,
) (ucan.Receipt, error) {
	t.Helper()
	inv := testutil.Must(blobcmds.Abort.Invoke(
		testutil.Alice,
		space.DID(),
		&blobcmds.AbortArguments{Space: space.DID(), Digest: digest, Cause: cause},
		invocation.WithAudience(uploadService.DID()),
	))(t)
	req := execution.NewRequest(t.Context(), inv)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
	require.NoError(t, err)
	if err := deps.handler.Handler(req, res); err != nil {
		return nil, err
	}
	return res.Receipt(), nil
}

func TestBlobAbortHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	uploadService := testutil.WebService

	t.Run("forwards to the parked blob's provider", func(t *testing.T) {
		deps := newBlobAbortTestDeps(t, uploadService, logger)
		space := testutil.RandomIssuer(t)
		blob := blobcmds.Blob{Digest: testutil.RandomMultihash(t), Size: 1024}

		storageProvider := testutil.RandomIssuer(t)
		piriSrv := newMockPiriRejectServer(t, storageProvider, identity.Identity{Issuer: uploadService})
		piriURL := testutil.Must(url.Parse(piriSrv.srv.URL))(t)
		rejectProof := testutil.Must(blobcmds.Reject.Delegate(storageProvider, uploadService.DID(), storageProvider.DID()))(t)
		require.NoError(t, deps.spStore.Put(t.Context(), storageProvider.DID(), *piriURL, 100, nil,
			container.New(container.WithDelegations(rejectProof))))

		cause := seedParkedBlobChain(t, deps.agentStore, uploadService, storageProvider, space.DID(), blob)

		rcpt, herr := invokeBlobAbort(t, deps, uploadService, space, blob.Digest, cause)
		require.NoError(t, herr)
		_, err := blobcmds.Abort.Unpack(rcpt)
		require.NoError(t, err)

		calls := piriSrv.Calls()
		require.Len(t, calls, 1, "abort forwarded to the provider as /blob/reject")
		require.Equal(t, space.DID(), calls[0].Space)
		require.Equal(t, blob.Digest, calls[0].Digest)
	})

	t.Run("missing cause is unrepresentable", func(t *testing.T) {
		// AbortArguments.Cause is a required (non-pointer) field: an
		// invocation without it cannot even be marshaled, so the provider
		// lookup can always rely on it. (The handler keeps a defensive
		// MissingCause failure for non-binding clients.)
		space := testutil.RandomIssuer(t)
		_, err := blobcmds.Abort.Invoke(
			testutil.Alice,
			space.DID(),
			&blobcmds.AbortArguments{Space: space.DID(), Digest: testutil.RandomMultihash(t), Cause: cid.Undef},
			invocation.WithAudience(uploadService.DID()),
		)
		require.ErrorContains(t, err, "undefined cid")
	})

	t.Run("unknown cause propagates the error", func(t *testing.T) {
		deps := newBlobAbortTestDeps(t, uploadService, logger)
		space := testutil.RandomIssuer(t)
		cause := testutil.RandomCID(t)

		_, herr := invokeBlobAbort(t, deps, uploadService, space, testutil.RandomMultihash(t), cause)
		require.Error(t, herr, "no receipt chain for the cause — caller can retry")
	})
}
