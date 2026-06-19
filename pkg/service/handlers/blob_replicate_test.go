package handlers_test

// import (
// 	"context"
// 	"fmt"
// 	"net/url"
// 	"testing"
// 	"time"

// 	"github.com/fil-forge/libforge/attestation/didmailto"
// 	"github.com/fil-forge/ucantone/did"
// 	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
// 	"github.com/storacha/go-libstoracha/capabilities/assert"
// 	blobreplicacap "github.com/storacha/go-libstoracha/capabilities/blob/replica"
// 	spaceblobcap "github.com/storacha/go-libstoracha/capabilities/space/blob"
// 	"github.com/storacha/go-libstoracha/capabilities/types"
// 	uclient "github.com/storacha/go-ucanto/client"
// 	"github.com/storacha/go-ucanto/core/delegation"
// 	"github.com/storacha/go-ucanto/core/invocation"
// 	"github.com/storacha/go-ucanto/core/receipt/fx"
// 	"github.com/storacha/go-ucanto/core/result"
// 	"github.com/storacha/go-ucanto/core/result/failure"
// 	"github.com/storacha/go-ucanto/core/result/failure/datamodel"
// 	"github.com/storacha/go-ucanto/principal"
// 	"github.com/storacha/go-ucanto/principal/signer"
// 	"github.com/storacha/go-ucanto/server"
// 	"github.com/storacha/go-ucanto/ucan"
// 	"github.com/storacha/go-ucanto/validator"
// 	"github.com/fil-forge/sprue/internal/config"
// 	"github.com/fil-forge/sprue/internal/testutil"
//
// 	"github.com/fil-forge/sprue/pkg/piriclient"
// 	"github.com/fil-forge/sprue/pkg/routing"
// 	"github.com/fil-forge/sprue/pkg/service/handlers"
// 	agent_store "github.com/fil-forge/sprue/pkg/store/agent/memory"
// 	"github.com/fil-forge/sprue/pkg/store/replica"
// 	replica_store "github.com/fil-forge/sprue/pkg/store/replica/memory"
// 	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
// 	"github.com/stretchr/testify/require"
// 	"go.uber.org/zap"
// 	"go.uber.org/zap/zaptest"
// )

// func TestSpaceBlobReplicateHandler(t *testing.T) {
// 	logger := zaptest.NewLogger(t)
// 	ctx := t.Context()

// 	alice := testutil.Alice
// 	aliceAccount := testutil.Must(didmailto.Parse("did:mailto:example.com:alice"))(t)
// 	uploadService := testutil.WebService

// 	defaultCfg := config.DeploymentConfig{MaxReplicas: 3}

// 	t.Run("invalid space DID", func(t *testing.T) {
// 		spStore := storage_provider_store.New()
// 		router := routing.NewService(spStore, logger)
// 		blobReg, _ := newBlobRegistry()
// 		replicaStore := replica_store.New()
// 		agentStore := agent_store.New()
// 		nodeProvider := piriclient.NewProvider(uploadService, logger)

// 		handler := handlers.SpaceBlobReplicateHandler(
// 			defaultCfg, identity.Identity{Signer: uploadService},
// 			router, blobReg, replicaStore, agentStore, nodeProvider, logger,
// 		)

// 		digest := testutil.RandomMultihash(t)
// 		blob := types.Blob{Digest: digest, Size: 1024}
// 		site := cidlink.Link{Cid: testutil.RandomCID(t)}

// 		cap := ucan.NewCapability(
// 			spaceblobcap.ReplicateAbility,
// 			"not-a-did",
// 			spaceblobcap.ReplicateCaveats{Blob: blob, Replicas: 2, Site: site},
// 		)

// 		inv, err := invocation.Invoke(alice, uploadService, cap)
// 		require.NoError(t, err)

// 		res, _, err := handler(ctx, cap, inv, nil)
// 		require.NoError(t, err)

// 		_, fail := result.Unwrap(res)
// 		require.NotNil(t, fail)

// 		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
// 		require.NotNil(t, model.Name)
// 		require.Equal(t, handlers.InvalidSpaceErrorName, *model.Name)
// 	})

// 	t.Run("replicas exceeds max", func(t *testing.T) {
// 		spStore := storage_provider_store.New()
// 		router := routing.NewService(spStore, logger)
// 		blobReg, _ := newBlobRegistry()
// 		replicaStore := replica_store.New()
// 		agentStore := agent_store.New()
// 		nodeProvider := piriclient.NewProvider(uploadService, logger)

// 		cfg := config.DeploymentConfig{MaxReplicas: 2}
// 		handler := handlers.SpaceBlobReplicateHandler(
// 			cfg, identity.Identity{Signer: uploadService},
// 			router, blobReg, replicaStore, agentStore, nodeProvider, logger,
// 		)

// 		space := testutil.RandomSigner(t)
// 		digest := testutil.RandomMultihash(t)
// 		blob := types.Blob{Digest: digest, Size: 1024}
// 		site := cidlink.Link{Cid: testutil.RandomCID(t)}

// 		cap := ucan.NewCapability(
// 			spaceblobcap.ReplicateAbility,
// 			space.DID().String(),
// 			spaceblobcap.ReplicateCaveats{Blob: blob, Replicas: 3, Site: site},
// 		)

// 		inv, err := invocation.Invoke(alice, uploadService, cap)
// 		require.NoError(t, err)

// 		res, _, err := handler(ctx, cap, inv, nil)
// 		require.NoError(t, err)

// 		_, fail := result.Unwrap(res)
// 		require.NotNil(t, fail)

// 		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
// 		require.NotNil(t, model.Name)
// 		require.Equal(t, handlers.ReplicationCountRangeErrorName, *model.Name)
// 	})

// 	t.Run("blob not found in space", func(t *testing.T) {
// 		spStore := storage_provider_store.New()
// 		router := routing.NewService(spStore, logger)
// 		blobReg, _ := newBlobRegistry()
// 		replicaStore := replica_store.New()
// 		agentStore := agent_store.New()
// 		nodeProvider := piriclient.NewProvider(uploadService, logger)

// 		handler := handlers.SpaceBlobReplicateHandler(
// 			defaultCfg, identity.Identity{Signer: uploadService},
// 			router, blobReg, replicaStore, agentStore, nodeProvider, logger,
// 		)

// 		space := testutil.RandomSigner(t)
// 		digest := testutil.RandomMultihash(t)
// 		blob := types.Blob{Digest: digest, Size: 1024}
// 		site := cidlink.Link{Cid: testutil.RandomCID(t)}

// 		cap := ucan.NewCapability(
// 			spaceblobcap.ReplicateAbility,
// 			space.DID().String(),
// 			spaceblobcap.ReplicateCaveats{Blob: blob, Replicas: 2, Site: site},
// 		)

// 		inv, err := invocation.Invoke(alice, uploadService, cap)
// 		require.NoError(t, err)

// 		res, _, err := handler(ctx, cap, inv, nil)
// 		require.NoError(t, err)

// 		_, fail := result.Unwrap(res)
// 		require.NotNil(t, fail)

// 		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
// 		require.NotNil(t, model.Name)
// 		require.Equal(t, handlers.ReplicationSourceNotFoundErrorName, *model.Name)
// 	})

// 	t.Run("invalid location commitment", func(t *testing.T) {
// 		spStore := storage_provider_store.New()
// 		router := routing.NewService(spStore, logger)
// 		blobReg, consumerStore := newBlobRegistry()
// 		replicaStore := replica_store.New()
// 		agentStore := agent_store.New()
// 		nodeProvider := piriclient.NewProvider(uploadService, logger)

// 		handler := handlers.SpaceBlobReplicateHandler(
// 			defaultCfg, identity.Identity{Signer: uploadService},
// 			router, blobReg, replicaStore, agentStore, nodeProvider, logger,
// 		)

// 		space := testutil.RandomSigner(t)

// 		// provision the space
// 		err := consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "", testutil.RandomCID(t))
// 		require.NoError(t, err)

// 		digest := testutil.RandomMultihash(t)
// 		blob := types.Blob{Digest: digest, Size: 1024}
// 		// random CID is not a valid location commitment delegation
// 		site := cidlink.Link{Cid: testutil.RandomCID(t)}

// 		// register the blob in the space
// 		err = blobReg.Register(ctx, space.DID(), blob, testutil.RandomCID(t))
// 		require.NoError(t, err)

// 		cap := ucan.NewCapability(
// 			spaceblobcap.ReplicateAbility,
// 			space.DID().String(),
// 			spaceblobcap.ReplicateCaveats{Blob: blob, Replicas: 2, Site: site},
// 		)

// 		inv, err := invocation.Invoke(alice, uploadService, cap)
// 		require.NoError(t, err)

// 		res, _, err := handler(ctx, cap, inv, nil)
// 		require.NoError(t, err)

// 		_, fail := result.Unwrap(res)
// 		require.NotNil(t, fail)

// 		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
// 		require.NotNil(t, model.Name)
// 		require.Equal(t, handlers.InvalidReplicationSiteErrorName, *model.Name)
// 	})

// 	t.Run("successful replication with new allocation", func(t *testing.T) {
// 		uploadID := testutil.Must(identity.New(""))(t)
// 		uploadService := uploadID.Signer

// 		// primary storage provider (already has the blob)
// 		primaryProvider := testutil.RandomSigner(t)

// 		// two replication providers (will each receive a replica)
// 		replicaProviderA := testutil.RandomSigner(t)
// 		replicaProviderAURL := testutil.Must(url.Parse("https://replica-a.example.com"))(t)
// 		replicaProviderAProof := delegateReplicaProviderProof(t, replicaProviderA, uploadService)

// 		replicaProviderB := testutil.RandomSigner(t)
// 		replicaProviderBURL := testutil.Must(url.Parse("https://replica-b.example.com"))(t)
// 		replicaProviderBProof := delegateReplicaProviderProof(t, replicaProviderB, uploadService)

// 		// register both replication providers in routing
// 		spStore := storage_provider_store.New()
// 		err := spStore.Put(ctx, *replicaProviderAURL, replicaProviderAProof, 100, nil)
// 		require.NoError(t, err)
// 		err = spStore.Put(ctx, *replicaProviderBURL, replicaProviderBProof, 100, nil)
// 		require.NoError(t, err)

// 		router := routing.NewService(spStore, logger)
// 		blobReg, consumerStore := newBlobRegistry()
// 		replicaStore := replica_store.New()
// 		agentStore := agent_store.New()

// 		// track which providers received allocations to verify exclusion
// 		var allocatedProviders []did.DID

// 		// mock handler for blob/replica/allocate
// 		replicaAllocHandler := func(
// 			ctx context.Context,
// 			cap ucan.Capability[blobreplicacap.AllocateCaveats],
// 			inv invocation.Invocation,
// 			iCtx server.InvocationContext,
// 		) (result.Result[blobreplicacap.AllocateOk, failure.IPLDBuilderFailure], fx.Effects, error) {
// 			allocatedProviders = append(allocatedProviders, iCtx.ID().DID())

// 			// create a transfer invocation to include in the response
// 			transferCap := ucan.NewCapability(
// 				"blob/replica/transfer",
// 				cap.With(),
// 				ucan.NoCaveats{},
// 			)
// 			transferInv, err := invocation.Invoke(iCtx.ID(), iCtx.ID(), transferCap)
// 			if err != nil {
// 				return nil, nil, err
// 			}

// 			ok := blobreplicacap.AllocateOk{
// 				Size: cap.Nb().Blob.Size,
// 				Site: types.Promise{
// 					UcanAwait: types.Await{
// 						Selector: blobreplicacap.AllocateSiteSelector,
// 						Link:     transferInv.Link(),
// 					},
// 				},
// 			}

// 			effects := fx.NewEffects(fx.WithFork(fx.FromInvocation(transferInv)))
// 			return result.Ok[blobreplicacap.AllocateOk, failure.IPLDBuilderFailure](ok), effects, nil
// 		}

// 		nodeProvider := newMultiMockReplicaNodeProvider(t, uploadService,
// 			[]ucan.Signer{replicaProviderA, replicaProviderB},
// 			replicaAllocHandler, logger,
// 		)

// 		handler := handlers.SpaceBlobReplicateHandler(
// 			defaultCfg, uploadID,
// 			router, blobReg, replicaStore, agentStore, nodeProvider, logger,
// 		)

// 		space := testutil.RandomSigner(t)

// 		// provision the space
// 		err = consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "", testutil.RandomCID(t))
// 		require.NoError(t, err)

// 		digest := testutil.RandomMultihash(t)
// 		blob := types.Blob{Digest: digest, Size: 1024}

// 		// register the blob in the space
// 		err = blobReg.Register(ctx, space.DID(), blob, testutil.RandomCID(t))
// 		require.NoError(t, err)

// 		// create a valid location commitment
// 		blobURL := testutil.Must(url.Parse("https://storage.example.com/blob"))(t)
// 		locationCap := assert.Location.New(primaryProvider.DID().String(), assert.LocationCaveats{
// 			Content:  types.FromHash(digest),
// 			Location: []url.URL{*blobURL},
// 			Space:    space.DID(),
// 		})
// 		lComm, err := delegation.Delegate(
// 			primaryProvider,
// 			uploadService,
// 			[]ucan.Capability[assert.LocationCaveats]{locationCap},
// 			delegation.WithExpiration(int(time.Now().Add(time.Hour).Unix())),
// 		)
// 		require.NoError(t, err)

// 		site := cidlink.Link{Cid: lComm.Link().(cidlink.Link).Cid}

// 		// request 3 replicas: source + 2 new allocations
// 		cap := ucan.NewCapability(
// 			spaceblobcap.ReplicateAbility,
// 			space.DID().String(),
// 			spaceblobcap.ReplicateCaveats{Blob: blob, Replicas: 3, Site: site},
// 		)

// 		inv, err := invocation.Invoke(alice, uploadService, cap)
// 		require.NoError(t, err)

// 		// attach the location commitment blocks to the invocation
// 		for b, bErr := range lComm.Blocks() {
// 			require.NoError(t, bErr)
// 			err = inv.Attach(b)
// 			require.NoError(t, err)
// 		}

// 		// create a server to get a valid InvocationContext
// 		srv, err := server.NewServer(uploadService)
// 		require.NoError(t, err)
// 		iCtx := srv.Context()

// 		res, effects, err := handler(ctx, cap, inv, iCtx)
// 		require.NoError(t, err)

// 		ok, fail := result.Unwrap(res)
// 		require.Nil(t, fail)
// 		require.NotNil(t, ok)

// 		// should have 2 site promises (one per new replica)
// 		require.Len(t, ok.Site, 2)
// 		for _, s := range ok.Site {
// 			require.Equal(t, blobreplicacap.AllocateSiteSelector, s.UcanAwait.Selector)
// 		}

// 		// should have effects
// 		require.NotNil(t, effects)

// 		// verify both replicas were recorded with distinct providers
// 		records, err := replicaStore.List(ctx, space.DID(), digest)
// 		require.NoError(t, err)
// 		require.Len(t, records, 2)
// 		require.NotEqual(t, records[0].Provider.DID(), records[1].Provider.DID())
// 		for _, r := range records {
// 			require.Equal(t, replica.Allocated, r.Status)
// 		}

// 		// verify allocations went to two distinct providers (exclusion worked)
// 		require.Len(t, allocatedProviders, 2)
// 		require.NotEqual(t, allocatedProviders[0], allocatedProviders[1])
// 	})

// 	t.Run("already fully replicated returns success", func(t *testing.T) {
// 		storageProvider := testutil.RandomSigner(t)
// 		storageProviderURL := testutil.Must(url.Parse("https://piri.example.com"))(t)
// 		storageProviderProof := delegateStorageProviderProof(t, storageProvider, uploadService)

// 		spStore := storage_provider_store.New()
// 		err := spStore.Put(ctx, *storageProviderURL, storageProviderProof, 100, nil)
// 		require.NoError(t, err)

// 		router := routing.NewService(spStore, logger)
// 		blobReg, consumerStore := newBlobRegistry()
// 		replicaStore := replica_store.New()
// 		agentStore := agent_store.New()
// 		nodeProvider := piriclient.NewProvider(uploadService, logger)

// 		handler := handlers.SpaceBlobReplicateHandler(
// 			defaultCfg, identity.Identity{Signer: uploadService},
// 			router, blobReg, replicaStore, agentStore, nodeProvider, logger,
// 		)

// 		space := testutil.RandomSigner(t)

// 		// provision the space
// 		err = consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "", testutil.RandomCID(t))
// 		require.NoError(t, err)

// 		digest := testutil.RandomMultihash(t)
// 		blob := types.Blob{Digest: digest, Size: 1024}
// 		site := cidlink.Link{Cid: testutil.RandomCID(t)}

// 		// register the blob in the space
// 		err = blobReg.Register(ctx, space.DID(), blob, testutil.RandomCID(t))
// 		require.NoError(t, err)

// 		// requesting 1 replica means source + 1 = 2 copies total
// 		// with 0 active replicas, newReplicasCount = 1 - (0 + 1) = 0
// 		// so no new allocations needed => success with no effects
// 		cap := ucan.NewCapability(
// 			spaceblobcap.ReplicateAbility,
// 			space.DID().String(),
// 			spaceblobcap.ReplicateCaveats{Blob: blob, Replicas: 1, Site: site},
// 		)

// 		inv, err := invocation.Invoke(alice, uploadService, cap)
// 		require.NoError(t, err)

// 		res, _, err := handler(ctx, cap, inv, nil)
// 		require.NoError(t, err)

// 		ok, fail := result.Unwrap(res)
// 		require.Nil(t, fail)
// 		require.NotNil(t, ok)
// 	})
// }

// // multiMockReplicaNodeProvider supports multiple storage node identities,
// // each backed by their own UCAN server for blob/replica/allocate.
// type multiMockReplicaNodeProvider struct {
// 	agentID ucan.Signer
// 	servers map[string]server.ServerView[server.Service]
// 	logger  *zap.Logger
// }

// func (m *multiMockReplicaNodeProvider) Client(id ucan.Principal, endpoint url.URL) (*piriclient.Client, error) {
// 	srv, ok := m.servers[id.DID().String()]
// 	if !ok {
// 		return nil, fmt.Errorf("no mock server for provider %s", id.DID())
// 	}
// 	conn, err := uclient.NewConnection(id, srv)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return piriclient.NewWithClient(id.DID(), m.agentID, conn, m.logger), nil
// }

// func newMultiMockReplicaNodeProvider(
// 	t *testing.T,
// 	agentID ucan.Signer,
// 	serviceIDs []ucan.Signer,
// 	allocHandler server.HandlerFunc[blobreplicacap.AllocateCaveats, blobreplicacap.AllocateOk, failure.IPLDBuilderFailure],
// 	logger *zap.Logger,
// ) *multiMockReplicaNodeProvider {
// 	t.Helper()

// 	servers := make(map[string]server.ServerView[server.Service], len(serviceIDs))
// 	for _, serviceID := range serviceIDs {
// 		ucanSrv, err := server.NewServer(
// 			serviceID,
// 			server.WithServiceMethod(
// 				blobreplicacap.AllocateAbility,
// 				server.Provide(blobreplicacap.Allocate, allocHandler),
// 			),
// 			server.WithPrincipalResolver(func(ctx context.Context, id did.DID) (did.DID, validator.UnresolvedDID) {
// 				if id == agentID.DID() {
// 					if ws, ok := agentID.(signer.WrappedSigner); ok {
// 						return ws.Unwrap().DID(), nil
// 					}
// 				}
// 				return validator.FailDIDKeyResolution(ctx, id)
// 			}),
// 		)
// 		require.NoError(t, err)
// 		servers[serviceID.DID().String()] = ucanSrv
// 	}

// 	return &multiMockReplicaNodeProvider{agentID: agentID, servers: servers, logger: logger}
// }

// // delegateReplicaProviderProof delegates blob/replica/allocate capability.
// func delegateReplicaProviderProof(t *testing.T, issuer ucan.Signer, audience ucan.Principal) delegation.Delegation {
// 	t.Helper()
// 	proof, err := delegation.Delegate(
// 		issuer,
// 		audience,
// 		[]ucan.Capability[ucan.NoCaveats]{
// 			ucan.NewCapability(blobreplicacap.Allocate.Can(), issuer.DID().String(), ucan.NoCaveats{}),
// 		},
// 	)
// 	require.NoError(t, err)
// 	return proof
// }
