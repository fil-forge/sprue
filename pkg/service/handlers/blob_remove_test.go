package handlers_test

import (
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/fil-forge/libforge/attestation/didmailto"
	blobcmds "github.com/fil-forge/libforge/commands/blob"
	httpcmds "github.com/fil-forge/libforge/commands/http"
	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	"github.com/fil-forge/sprue/pkg/store/agent"
	agent_store "github.com/fil-forge/sprue/pkg/store/agent/memory"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	blob_registry "github.com/fil-forge/sprue/pkg/store/blob_registry/memory"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	metrics_store "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	"github.com/fil-forge/sprue/pkg/store/replica"
	replica_store "github.com/fil-forge/sprue/pkg/store/replica/memory"
	spacediff_store "github.com/fil-forge/sprue/pkg/store/space_diff/memory"
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
	"github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

type blobRemoveTestDeps struct {
	handler       server.Route
	consumerStore *consumer_store.Store
	spStore       *storage_provider_store.Store
	agentStore    *agent_store.Store
	blobReg       *blob_registry.Store
	replicaStore  *replica_store.Store
}

func newBlobRemoveTestDeps(t *testing.T, uploadService multikey.Issuer, logger *zap.Logger) *blobRemoveTestDeps {
	t.Helper()
	spStore := storage_provider_store.New()
	router := routing.NewService(spStore, logger)
	agentStore := agent_store.New()
	consumerStore := consumer_store.New()
	blobReg := blob_registry.New(
		spacediff_store.New(),
		consumerStore,
		metrics_store.NewSpaceStore(),
		metrics_store.New(),
	)
	replicaStore := replica_store.New()
	nodeProvider := piriclient.NewProvider(uploadService, logger)
	handler := handlers.NewBlobRemoveHandler(
		router,
		nodeProvider,
		agentStore,
		blobReg,
		replicaStore,
		logger,
	)
	return &blobRemoveTestDeps{
		handler:       handler,
		consumerStore: consumerStore,
		spStore:       spStore,
		agentStore:    agentStore,
		blobReg:       blobReg,
		replicaStore:  replicaStore,
	}
}

// mockPiriRemoveServer serves /blob/remove and records the arguments of each
// call.
type mockPiriRemoveServer struct {
	srv *httptest.Server

	mu    sync.Mutex
	calls []blobcmds.RemoveArguments
}

func newMockPiriRemoveServer(t *testing.T, storageProvider ucan.Issuer, uploadService identity.Identity) *mockPiriRemoveServer {
	t.Helper()
	m := &mockPiriRemoveServer{}

	srv := server.NewHTTP(
		storageProvider,
		server.WithValidationOptions(validator.WithDIDResolver(resolver.Tiered{
			resolver.WellKnown{uploadService.DID(): testutil.Must(uploadService.DIDDocument())(t)},
			key.Resolver,
		})),
	)
	srv.Handle(blobcmds.Remove.Command, blobcmds.Remove.Handler(func(
		req *binding.Request[*blobcmds.RemoveArguments],
		res *binding.Response[*blobcmds.RemoveOK],
	) error {
		m.mu.Lock()
		m.calls = append(m.calls, *req.Task().Arguments())
		m.mu.Unlock()
		return res.SetSuccess(&blobcmds.RemoveOK{})
	}))

	m.srv = httptest.NewServer(srv)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockPiriRemoveServer) Calls() []blobcmds.RemoveArguments {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]blobcmds.RemoveArguments(nil), m.calls...)
}

// removeProviderProofs builds the registration proof container including the
// /blob/remove capability.
func removeProviderProofs(t *testing.T, storageProvider, uploadService ucan.Issuer) ucan.Container {
	t.Helper()
	removeProof := testutil.Must(blobcmds.Remove.Delegate(storageProvider, uploadService.DID(), storageProvider.DID()))(t)
	return container.New(container.WithDelegations(removeProof))
}

// registerStoredBlob persists the receipt chain a stored blob leaves behind
// (add → accept → put → allocate, with the accept invocation's subject being
// the storage provider) and registers the blob, so the remove handler can
// recover the primary provider. Returns the registration cause.
func registerStoredBlob(
	t *testing.T,
	deps *blobRemoveTestDeps,
	uploadService ucan.Issuer,
	storageProvider ucan.Issuer,
	space did.DID,
	blob blobcmds.Blob,
) {
	t.Helper()
	ctx := t.Context()

	// The registry's metrics accounting requires a consumer record for the
	// space.
	account := testutil.Must(didmailto.New("alice@example.com"))(t)
	require.NoError(t, deps.consumerStore.Add(ctx, uploadService.DID(), space, account, "sub-1", testutil.RandomCID(t)))

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
	// The accept invocation's subject is the provider — this is how the
	// remove handler recovers the primary.
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
	require.NoError(t, deps.agentStore.Write(ctx, msg, agent.Index(msg)))
	require.NoError(t, deps.blobReg.Register(ctx, space, blob, addInv.Task().Link()))
}

func invokeBlobRemove(
	t *testing.T,
	deps *blobRemoveTestDeps,
	uploadService ucan.Issuer,
	space ucan.Principal,
	digest multihash.Multihash,
) ucan.Receipt {
	t.Helper()
	inv := testutil.Must(blobcmds.Remove.Invoke(
		testutil.Alice,
		space.DID(),
		&blobcmds.RemoveArguments{Space: space.DID(), Digest: digest},
		invocation.WithAudience(uploadService.DID()),
	))(t)
	req := execution.NewRequest(t.Context(), inv)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
	require.NoError(t, err)
	require.NoError(t, deps.handler.Handler(req, res))
	return res.Receipt()
}

func TestBlobRemoveHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	uploadService := testutil.WebService

	t.Run("unregistered blob is idempotent success", func(t *testing.T) {
		deps := newBlobRemoveTestDeps(t, uploadService, logger)
		space := testutil.RandomIssuer(t)

		rcpt := invokeBlobRemove(t, deps, uploadService, space, testutil.RandomMultihash(t))
		_, err := blobcmds.Remove.Unpack(rcpt)
		require.NoError(t, err)
	})

	t.Run("forwards to primary provider and deregisters", func(t *testing.T) {
		deps := newBlobRemoveTestDeps(t, uploadService, logger)
		space := testutil.RandomIssuer(t)
		blob := blobcmds.Blob{Digest: testutil.RandomMultihash(t), Size: 1024}

		storageProvider := testutil.RandomIssuer(t)
		piriSrv := newMockPiriRemoveServer(t, storageProvider, identity.Identity{Issuer: uploadService})
		piriURL := testutil.Must(url.Parse(piriSrv.srv.URL))(t)
		require.NoError(t, deps.spStore.Put(t.Context(), storageProvider.DID(), *piriURL, 100, nil,
			removeProviderProofs(t, storageProvider, uploadService)))

		registerStoredBlob(t, deps, uploadService, storageProvider, space.DID(), blob)

		rcpt := invokeBlobRemove(t, deps, uploadService, space, blob.Digest)
		_, err := blobcmds.Remove.Unpack(rcpt)
		require.NoError(t, err)

		calls := piriSrv.Calls()
		require.Len(t, calls, 1, "removal forwarded to the primary provider")
		require.Equal(t, space.DID(), calls[0].Space)
		require.Equal(t, blob.Digest, calls[0].Digest)

		_, err = deps.blobReg.Get(t.Context(), space.DID(), blob.Digest)
		require.ErrorIs(t, err, blobregistry.ErrEntryNotFound, "blob deregistered")

		// Removing again is idempotent success and forwards nothing new.
		rcpt = invokeBlobRemove(t, deps, uploadService, space, blob.Digest)
		_, err = blobcmds.Remove.Unpack(rcpt)
		require.NoError(t, err)
		require.Len(t, piriSrv.Calls(), 1)
	})

	t.Run("forwards to replicas too", func(t *testing.T) {
		deps := newBlobRemoveTestDeps(t, uploadService, logger)
		space := testutil.RandomIssuer(t)
		blob := blobcmds.Blob{Digest: testutil.RandomMultihash(t), Size: 1024}

		primary := testutil.RandomIssuer(t)
		primarySrv := newMockPiriRemoveServer(t, primary, identity.Identity{Issuer: uploadService})
		primaryURL := testutil.Must(url.Parse(primarySrv.srv.URL))(t)
		require.NoError(t, deps.spStore.Put(t.Context(), primary.DID(), *primaryURL, 100, nil,
			removeProviderProofs(t, primary, uploadService)))

		replicaNode := testutil.RandomIssuer(t)
		replicaSrv := newMockPiriRemoveServer(t, replicaNode, identity.Identity{Issuer: uploadService})
		replicaURL := testutil.Must(url.Parse(replicaSrv.srv.URL))(t)
		require.NoError(t, deps.spStore.Put(t.Context(), replicaNode.DID(), *replicaURL, 100, nil,
			removeProviderProofs(t, replicaNode, uploadService)))

		registerStoredBlob(t, deps, uploadService, primary, space.DID(), blob)
		require.NoError(t, deps.replicaStore.Add(t.Context(), space.DID(), blob.Digest,
			replicaNode.DID(), replica.Transferred, testutil.RandomCID(t)))

		rcpt := invokeBlobRemove(t, deps, uploadService, space, blob.Digest)
		_, err := blobcmds.Remove.Unpack(rcpt)
		require.NoError(t, err)

		require.Len(t, primarySrv.Calls(), 1, "primary provider notified")
		require.Len(t, replicaSrv.Calls(), 1, "replica provider notified")
	})

	t.Run("unreachable provider does not fail the removal", func(t *testing.T) {
		deps := newBlobRemoveTestDeps(t, uploadService, logger)
		space := testutil.RandomIssuer(t)
		blob := blobcmds.Blob{Digest: testutil.RandomMultihash(t), Size: 1024}

		// The primary provider is registered with an endpoint nothing
		// listens on.
		storageProvider := testutil.RandomIssuer(t)
		deadURL := testutil.Must(url.Parse("http://127.0.0.1:1"))(t)
		require.NoError(t, deps.spStore.Put(t.Context(), storageProvider.DID(), *deadURL, 100, nil,
			removeProviderProofs(t, storageProvider, uploadService)))

		registerStoredBlob(t, deps, uploadService, storageProvider, space.DID(), blob)

		rcpt := invokeBlobRemove(t, deps, uploadService, space, blob.Digest)
		_, err := blobcmds.Remove.Unpack(rcpt)
		require.NoError(t, err, "forwarding is best-effort")

		_, err = deps.blobReg.Get(t.Context(), space.DID(), blob.Digest)
		require.ErrorIs(t, err, blobregistry.ErrEntryNotFound, "blob still deregistered")
	})
}
