package handlers_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	blobcap "github.com/fil-forge/go-libstoracha/capabilities/blob"
	spaceblobcap "github.com/fil-forge/go-libstoracha/capabilities/space/blob"
	"github.com/fil-forge/go-libstoracha/capabilities/types"
	uclient "github.com/fil-forge/go-ucanto/client"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/principal"
	"github.com/fil-forge/go-ucanto/principal/signer"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/go-ucanto/validator"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/lib/didmailto"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	agent_store "github.com/fil-forge/sprue/pkg/store/agent/memory"
	blob_registry "github.com/fil-forge/sprue/pkg/store/blob_registry/memory"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	metrics_store "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	spacediff_store "github.com/fil-forge/sprue/pkg/store/space_diff/memory"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func newBlobRegistry() (*blob_registry.Store, *consumer_store.Store) {
	consumerStore := consumer_store.New()
	return blob_registry.New(
		spacediff_store.New(),
		consumerStore,
		metrics_store.NewSpaceStore(),
		metrics_store.New(),
	), consumerStore
}

// mockNodeProvider is a NodeProvider that creates piri clients connected
// directly to a UCAN server (which implements transport.Channel), bypassing
// HTTP entirely.
type mockNodeProvider struct {
	signer ucan.Signer
	srv    server.ServerView[server.Service]
	logger *zap.Logger
}

func (m *mockNodeProvider) Client(id ucan.Principal, endpoint url.URL) (*piriclient.Client, error) {
	// the ID must match the ID of the server this provider started
	if id.DID() != m.srv.ID().DID() {
		return nil, fmt.Errorf("unexpected client ID %s, expected %s", id.DID(), m.signer.DID())
	}
	conn, err := uclient.NewConnection(id, m.srv)
	if err != nil {
		return nil, err
	}
	return piriclient.NewWithConnection(id.DID(), m.signer, conn, m.logger), nil
}

// newMockNodeProvider creates a mock NodeProvider backed by a UCAN server.
// The server is created with the serviceID so that audience validation passes
// (doAllocate passes the upload service signer as the connection audience).
func newMockNodeProvider(
	t *testing.T,
	agentID ucan.Signer, // the ID of the upload service (signer of invocations)
	serviceID principal.Signer, // the ID of the storage node (signer of receipts)
	allocHandler server.HandlerFunc[blobcap.AllocateCaveats, blobcap.AllocateOk, failure.IPLDBuilderFailure],
	acceptHandler server.HandlerFunc[blobcap.AcceptCaveats, blobcap.AcceptOk, failure.IPLDBuilderFailure],
	logger *zap.Logger,
) *mockNodeProvider {
	t.Helper()

	ucanSrv, err := server.NewServer(
		serviceID,
		server.WithServiceMethod(
			blobcap.AllocateAbility,
			server.Provide(blobcap.Allocate, allocHandler),
		),
		server.WithServiceMethod(
			blobcap.AcceptAbility,
			server.Provide(blobcap.Accept, acceptHandler),
		),
		server.WithPrincipalResolver(func(ctx context.Context, id did.DID) (did.DID, validator.UnresolvedDID) {
			if id == agentID.DID() {
				if ws, ok := agentID.(signer.WrappedSigner); ok {
					return ws.Unwrap().DID(), nil
				}
			}
			return validator.FailDIDKeyResolution(ctx, id)
		}),
	)
	require.NoError(t, err)

	return &mockNodeProvider{signer: agentID, srv: ucanSrv, logger: logger}
}

func newOkHandler[Caveats any, Ok ipld.Builder](t *testing.T, ok Ok) server.HandlerFunc[Caveats, Ok, failure.IPLDBuilderFailure] {
	t.Helper()
	return func(ctx context.Context, cap ucan.Capability[Caveats], inv invocation.Invocation, iCtx server.InvocationContext,
	) (result.Result[Ok, failure.IPLDBuilderFailure], fx.Effects, error) {
		return result.Ok[Ok, failure.IPLDBuilderFailure](ok), nil, nil
	}
}

// Delegates blob/allocate and blob/accept
func delegateStorageProviderProof(t *testing.T, issuer principal.Signer, audience ucan.Principal) delegation.Delegation {
	t.Helper()
	proof, err := delegation.Delegate(
		issuer,
		audience,
		[]ucan.Capability[ucan.NoCaveats]{
			ucan.NewCapability(blobcap.Allocate.Can(), issuer.DID().String(), ucan.NoCaveats{}),
			ucan.NewCapability(blobcap.Accept.Can(), issuer.DID().String(), ucan.NoCaveats{}),
		},
	)
	require.NoError(t, err)
	return proof
}

func TestSpaceBlobAddHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	alice := testutil.Alice
	aliceAccount := testutil.Must(didmailto.Parse("did:mailto:example.com:alice"))(t)
	uploadService := testutil.WebService
	storageProvider := testutil.RandomSigner(t)
	storageProviderURL := testutil.Must(url.Parse("https://piri.example.com"))(t)
	storageProviderProof := delegateStorageProviderProof(t, storageProvider, uploadService)

	t.Run("invalid space DID", func(t *testing.T) {
		spStore := storage_provider_store.New()
		router := routing.NewService(spStore, logger)
		agentStore := agent_store.New()
		blobReg, _ := newBlobRegistry()

		nodeProvider := piriclient.NewProvider(uploadService, logger)
		handler := handlers.SpaceBlobAddHandler(&identity.Identity{Signer: uploadService}, router, nodeProvider, agentStore, blobReg, logger)

		digest := testutil.RandomMultihash(t)
		blob := types.Blob{Digest: digest, Size: 1024}

		cap := ucan.NewCapability(
			spaceblobcap.AddAbility,
			"not-a-did",
			spaceblobcap.AddCaveats{Blob: blob},
		)

		inv, err := invocation.Invoke(alice, uploadService, cap)
		require.NoError(t, err)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("no candidates available", func(t *testing.T) {
		spStore := storage_provider_store.New()
		router := routing.NewService(spStore, logger)
		agentStore := agent_store.New()
		blobReg, _ := newBlobRegistry()

		nodeProvider := piriclient.NewProvider(uploadService, logger)
		handler := handlers.SpaceBlobAddHandler(&identity.Identity{Signer: uploadService}, router, nodeProvider, agentStore, blobReg, logger)

		space := testutil.RandomSigner(t)
		digest := testutil.RandomMultihash(t)
		blob := types.Blob{Digest: digest, Size: 1024}

		cap := ucan.NewCapability(
			spaceblobcap.AddAbility,
			space.DID().String(),
			spaceblobcap.AddCaveats{Blob: blob},
		)

		inv, err := invocation.Invoke(alice, uploadService, cap)
		require.NoError(t, err)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("zero weight providers returns candidate unavailable", func(t *testing.T) {
		spStore := storage_provider_store.New()
		err := spStore.Put(ctx, *storageProviderURL, storageProviderProof, 0, nil)
		require.NoError(t, err)

		router := routing.NewService(spStore, logger)
		agentStore := agent_store.New()
		blobReg, _ := newBlobRegistry()

		nodeProvider := piriclient.NewProvider(uploadService, logger)
		handler := handlers.SpaceBlobAddHandler(&identity.Identity{Signer: uploadService}, router, nodeProvider, agentStore, blobReg, logger)

		space := testutil.RandomSigner(t)
		digest := testutil.RandomMultihash(t)
		blob := types.Blob{Digest: digest, Size: 1024}

		cap := ucan.NewCapability(
			spaceblobcap.AddAbility,
			space.DID().String(),
			spaceblobcap.AddCaveats{Blob: blob},
		)

		inv, err := invocation.Invoke(alice, uploadService, cap)
		require.NoError(t, err)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("successful allocation with address", func(t *testing.T) {
		putURL := testutil.Must(url.Parse("https://storage.example.com/put"))(t)
		allocateOk := blobcap.AllocateOk{
			Size: 1024,
			Address: &blobcap.Address{
				URL:     *putURL,
				Expires: 9999999999,
			},
		}

		acceptOk := blobcap.AcceptOk{Site: cidlink.Link{Cid: testutil.RandomCID(t)}}

		nodeProvider := newMockNodeProvider(
			t,
			uploadService,
			storageProvider,
			newOkHandler[blobcap.AllocateCaveats](t, allocateOk),
			newOkHandler[blobcap.AcceptCaveats](t, acceptOk),
			logger,
		)

		spStore := storage_provider_store.New()
		err := spStore.Put(ctx, *storageProviderURL, storageProviderProof, 100, nil)
		require.NoError(t, err)

		router := routing.NewService(spStore, logger)
		agentStore := agent_store.New()
		blobReg, consumerStore := newBlobRegistry()

		handler := handlers.SpaceBlobAddHandler(&identity.Identity{Signer: uploadService}, router, nodeProvider, agentStore, blobReg, logger)

		space := testutil.RandomSigner(t)
		digest := testutil.RandomMultihash(t)
		blob := types.Blob{Digest: digest, Size: 1024}

		// mock "provision" the space, by adding it to the consumer store
		err = consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "", testutil.RandomCID(t))
		require.NoError(t, err)

		cap := ucan.NewCapability(
			spaceblobcap.AddAbility,
			space.DID().String(),
			spaceblobcap.AddCaveats{Blob: blob},
		)

		// we don't need proof alice can invoke this capability since the handler
		// is being tested directly and no validation is performed
		inv, err := invocation.Invoke(alice, uploadService, cap)
		require.NoError(t, err)

		res, effects, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.NotNil(t, effects)
	})

	t.Run("successful allocation blob already stored", func(t *testing.T) {
		// No address means blob is already on the provider
		allocateOk := blobcap.AllocateOk{Size: 1024, Address: nil}
		acceptOk := blobcap.AcceptOk{Site: cidlink.Link{Cid: testutil.RandomCID(t)}}

		nodeProvider := newMockNodeProvider(
			t,
			uploadService,
			storageProvider,
			newOkHandler[blobcap.AllocateCaveats](t, allocateOk),
			newOkHandler[blobcap.AcceptCaveats](t, acceptOk),
			logger,
		)

		spStore := storage_provider_store.New()
		err := spStore.Put(ctx, *storageProviderURL, storageProviderProof, 100, nil)
		require.NoError(t, err)

		router := routing.NewService(spStore, logger)
		agentStore := agent_store.New()
		blobReg, consumerStore := newBlobRegistry()

		handler := handlers.SpaceBlobAddHandler(&identity.Identity{Signer: uploadService}, router, nodeProvider, agentStore, blobReg, logger)

		space := testutil.RandomSigner(t)
		digest := testutil.RandomMultihash(t)
		blob := types.Blob{Digest: digest, Size: 1024}

		// mock "provision" the space, by adding it to the consumer store
		err = consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "", testutil.RandomCID(t))
		require.NoError(t, err)

		cap := ucan.NewCapability(
			spaceblobcap.AddAbility,
			space.DID().String(),
			spaceblobcap.AddCaveats{Blob: blob},
		)

		// we don't need proof alice can invoke this capability since the handler
		// is being tested directly and no validation is performed
		inv, err := invocation.Invoke(alice, uploadService, cap)
		require.NoError(t, err)

		res, effects, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.Nil(t, fail)
		// Should have effects including accept since blob was already stored
		require.NotNil(t, effects)
	})
}
