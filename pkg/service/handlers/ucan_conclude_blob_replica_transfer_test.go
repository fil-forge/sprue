package handlers_test

// import (
// 	"io"
// 	"testing"

// 	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
// 	cmdreplica "github.com/storacha/go-libstoracha/capabilities/blob/replica"
// 	"github.com/storacha/go-libstoracha/capabilities/types"
// 	"github.com/storacha/go-ucanto/core/car"
// 	"github.com/storacha/go-ucanto/core/delegation"
// 	"github.com/storacha/go-ucanto/core/invocation"
// 	"github.com/storacha/go-ucanto/core/ipld"
// 	"github.com/storacha/go-ucanto/core/message"
// 	"github.com/storacha/go-ucanto/core/receipt"
// 	"github.com/storacha/go-ucanto/core/receipt/ran"
// 	"github.com/storacha/go-ucanto/core/result"
// 	"github.com/storacha/go-ucanto/core/result/ok"
// 	"github.com/storacha/go-ucanto/server"
// 	"github.com/storacha/go-ucanto/ucan"
// 	"github.com/fil-forge/sprue/internal/testutil"
// 	"github.com/fil-forge/sprue/pkg/identity"
// 	"github.com/fil-forge/sprue/pkg/service/handlers"
// 	"github.com/fil-forge/sprue/pkg/store/agent"
// 	agent_store "github.com/fil-forge/sprue/pkg/store/agent/memory"
// 	"github.com/fil-forge/sprue/pkg/store/replica"
// 	replica_store "github.com/fil-forge/sprue/pkg/store/replica/memory"
// 	"github.com/stretchr/testify/require"
// 	"go.uber.org/zap/zaptest"
// )

// // writeAgentMessage writes invocations and receipts to the agent store using
// // the same pattern as the production writeAgentMessage helper.
// func writeAgentMessage(t *testing.T, store agent.Store, invocations []invocation.Invocation, receipts []receipt.AnyReceipt) {
// 	t.Helper()
// 	msg, err := message.Build(invocations, receipts)
// 	require.NoError(t, err)

// 	var idx []agent.IndexEntry
// 	for entry, err := range agent.Index(msg) {
// 		require.NoError(t, err)
// 		idx = append(idx, entry)
// 	}

// 	src, err := io.ReadAll(car.Encode([]ipld.Link{msg.Root().Link()}, msg.Blocks()))
// 	require.NoError(t, err)

// 	err = store.Write(t.Context(), msg, idx, src)
// 	require.NoError(t, err)
// }

// func mockInvocationContext(t *testing.T) server.InvocationContext {
// 	t.Helper()
// 	s, err := server.NewServer(testutil.RandomSigner(t))
// 	require.NoError(t, err)
// 	return s.Context()
// }

// func TestBlobReplicaTransferConcludeHandler(t *testing.T) {
// 	logger := zaptest.NewLogger(t)
// 	ctx := t.Context()

// 	uploadService := testutil.WebService
// 	storageProvider := testutil.RandomSigner(t)

// 	t.Run("invalid transfer parameters", func(t *testing.T) {
// 		agentStore := agent_store.New()
// 		replicaStore := replica_store.New()

// 		ch := handlers.NewBlobReplicaTransferConcludeHandler(
// 			&identity.Identity{Signer: uploadService}, agentStore, replicaStore, logger,
// 		)

// 		// Create an invocation with wrong capability (not blob/replica/transfer)
// 		cap := ucan.NewCapability(
// 			"blob/allocate",
// 			storageProvider.DID().String(),
// 			ucan.NoCaveats{},
// 		)
// 		transferInv, err := invocation.Invoke(storageProvider, uploadService, cap)
// 		require.NoError(t, err)

// 		transferRcpt, err := receipt.Issue(
// 			storageProvider,
// 			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
// 			ran.FromInvocation(transferInv),
// 		)
// 		require.NoError(t, err)

// 		err = ch.Handler(ctx, transferInv, transferRcpt, nil)
// 		require.Error(t, err)
// 		require.Contains(t, err.Error(), "invalid replica transfer parameters")
// 	})

// 	t.Run("allocation invocation not found", func(t *testing.T) {
// 		agentStore := agent_store.New()
// 		replicaStore := replica_store.New()

// 		ch := handlers.NewBlobReplicaTransferConcludeHandler(
// 			&identity.Identity{Signer: uploadService}, agentStore, replicaStore, logger,
// 		)

// 		space := testutil.RandomSigner(t)
// 		digest := testutil.RandomMultihash(t)
// 		blob := types.Blob{Digest: digest, Size: 1024}
// 		site := cidlink.Link{Cid: testutil.RandomCID(t)}
// 		// cause points to a non-existent allocation invocation
// 		cause := cidlink.Link{Cid: testutil.RandomCID(t)}

// 		transferCap := ucan.NewCapability(
// 			cmdreplica.TransferAbility,
// 			storageProvider.DID().String(),
// 			cmdreplica.TransferCaveats{
// 				Space: space.DID(),
// 				Blob:  blob,
// 				Site:  site,
// 				Cause: cause,
// 			},
// 		)
// 		transferInv, err := invocation.Invoke(storageProvider, uploadService, transferCap)
// 		require.NoError(t, err)

// 		transferRcpt, err := receipt.Issue(
// 			storageProvider,
// 			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
// 			ran.FromInvocation(transferInv),
// 		)
// 		require.NoError(t, err)

// 		err = ch.Handler(ctx, transferInv, transferRcpt, nil)
// 		require.Error(t, err)
// 		require.Contains(t, err.Error(), "getting replica allocation invocation")
// 	})

// 	t.Run("allocation not signed by service", func(t *testing.T) {
// 		agentStore := agent_store.New()
// 		replicaStore := replica_store.New()

// 		ch := handlers.NewBlobReplicaTransferConcludeHandler(
// 			&identity.Identity{Signer: uploadService}, agentStore, replicaStore, logger,
// 		)

// 		space := testutil.RandomSigner(t)
// 		digest := testutil.RandomMultihash(t)
// 		blob := types.Blob{Digest: digest, Size: 1024}
// 		site := cidlink.Link{Cid: testutil.RandomCID(t)}
// 		replicateCause := cidlink.Link{Cid: testutil.RandomCID(t)}

// 		// Create allocation invocation signed by someone OTHER than uploadService
// 		imposter := testutil.RandomSigner(t)
// 		allocCap := ucan.NewCapability(
// 			cmdreplica.AllocateAbility,
// 			storageProvider.DID().String(),
// 			cmdreplica.AllocateCaveats{
// 				Space: space.DID(),
// 				Blob:  blob,
// 				Site:  site,
// 				Cause: replicateCause,
// 			},
// 		)
// 		allocInv, err := invocation.Invoke(imposter, storageProvider, allocCap)
// 		require.NoError(t, err)

// 		allocRcpt, err := receipt.Issue(
// 			storageProvider,
// 			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
// 			ran.FromInvocation(allocInv),
// 		)
// 		require.NoError(t, err)

// 		// Store the allocation in the agent store
// 		writeAgentMessage(t, agentStore, []invocation.Invocation{allocInv}, []receipt.AnyReceipt{allocRcpt})

// 		// Create transfer invocation referencing the allocation
// 		transferCap := ucan.NewCapability(
// 			cmdreplica.TransferAbility,
// 			storageProvider.DID().String(),
// 			cmdreplica.TransferCaveats{
// 				Space: space.DID(),
// 				Blob:  blob,
// 				Site:  site,
// 				Cause: allocInv.Link(),
// 			},
// 		)
// 		transferInv, err := invocation.Invoke(storageProvider, uploadService, transferCap)
// 		require.NoError(t, err)

// 		transferRcpt, err := receipt.Issue(
// 			storageProvider,
// 			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
// 			ran.FromInvocation(transferInv),
// 		)
// 		require.NoError(t, err)

// 		err = ch.Handler(ctx, transferInv, transferRcpt, nil)
// 		require.Error(t, err)
// 		require.Contains(t, err.Error(), "allocation was not issued by this service")
// 	})

// 	t.Run("success updates replica status to transferred", func(t *testing.T) {
// 		agentStore := agent_store.New()
// 		replicaStore := replica_store.New()

// 		ch := handlers.NewBlobReplicaTransferConcludeHandler(
// 			&identity.Identity{Signer: uploadService}, agentStore, replicaStore, logger,
// 		)

// 		space := testutil.RandomSigner(t)
// 		digest := testutil.RandomMultihash(t)
// 		blob := types.Blob{Digest: digest, Size: 1024}
// 		site := cidlink.Link{Cid: testutil.RandomCID(t)}
// 		replicateCause := cidlink.Link{Cid: testutil.RandomCID(t)}

// 		// storageProvider delegates to uploadService so it can invoke allocate
// 		allocProof, err := delegation.Delegate(
// 			storageProvider, uploadService,
// 			[]ucan.Capability[ucan.NoCaveats]{
// 				ucan.NewCapability(
// 					cmdreplica.AllocateAbility,
// 					storageProvider.DID().String(),
// 					ucan.NoCaveats{},
// 				),
// 			},
// 		)
// 		require.NoError(t, err)

// 		// uploadService invokes allocate on storageProvider with the delegation as proof
// 		allocInv, err := cmdreplica.Allocate.Invoke(
// 			uploadService, storageProvider,
// 			storageProvider.DID().String(),
// 			cmdreplica.AllocateCaveats{
// 				Space: space.DID(),
// 				Blob:  blob,
// 				Site:  site,
// 				Cause: replicateCause,
// 			},
// 			delegation.WithProof(delegation.FromDelegation(allocProof)),
// 		)
// 		require.NoError(t, err)

// 		allocRcpt, err := receipt.Issue(
// 			storageProvider,
// 			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
// 			ran.FromInvocation(allocInv),
// 		)
// 		require.NoError(t, err)

// 		writeAgentMessage(t, agentStore, []invocation.Invocation{allocInv}, []receipt.AnyReceipt{allocRcpt})

// 		// Add a replica record so SetStatus succeeds
// 		err = replicaStore.Add(ctx, space.DID(), digest, storageProvider.DID(), replica.Allocated, testutil.RandomCID(t))
// 		require.NoError(t, err)

// 		// storageProvider self-issues the transfer invocation
// 		transferInv, err := cmdreplica.Transfer.Invoke(
// 			storageProvider, storageProvider,
// 			storageProvider.DID().String(),
// 			cmdreplica.TransferCaveats{
// 				Space: space.DID(),
// 				Blob:  blob,
// 				Site:  site,
// 				Cause: allocInv.Link(),
// 			},
// 		)
// 		require.NoError(t, err)

// 		// Receipt signed by storageProvider (the executor)
// 		transferRcpt, err := receipt.Issue(
// 			storageProvider,
// 			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
// 			ran.FromInvocation(transferInv),
// 		)
// 		require.NoError(t, err)

// 		iCtx := mockInvocationContext(t)
// 		err = ch.Handler(ctx, transferInv, transferRcpt, iCtx)
// 		require.NoError(t, err)

// 		// Verify the replica status was updated to Transferred
// 		records, err := replicaStore.List(ctx, space.DID(), digest)
// 		require.NoError(t, err)
// 		require.Len(t, records, 1)
// 		require.Equal(t, replica.Transferred, records[0].Status)
// 	})

// 	t.Run("executor mismatch", func(t *testing.T) {
// 		agentStore := agent_store.New()
// 		replicaStore := replica_store.New()

// 		ch := handlers.NewBlobReplicaTransferConcludeHandler(
// 			&identity.Identity{Signer: uploadService}, agentStore, replicaStore, logger,
// 		)

// 		space := testutil.RandomSigner(t)
// 		digest := testutil.RandomMultihash(t)
// 		blob := types.Blob{Digest: digest, Size: 1024}
// 		site := cidlink.Link{Cid: testutil.RandomCID(t)}
// 		replicateCause := cidlink.Link{Cid: testutil.RandomCID(t)}

// 		// Create allocation invocation signed by uploadService, audience = storageProvider
// 		allocCap := ucan.NewCapability(
// 			cmdreplica.AllocateAbility,
// 			storageProvider.DID().String(),
// 			cmdreplica.AllocateCaveats{
// 				Space: space.DID(),
// 				Blob:  blob,
// 				Site:  site,
// 				Cause: replicateCause,
// 			},
// 		)
// 		allocInv, err := invocation.Invoke(uploadService, storageProvider, allocCap)
// 		require.NoError(t, err)

// 		allocRcpt, err := receipt.Issue(
// 			storageProvider,
// 			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
// 			ran.FromInvocation(allocInv),
// 		)
// 		require.NoError(t, err)

// 		writeAgentMessage(t, agentStore, []invocation.Invocation{allocInv}, []receipt.AnyReceipt{allocRcpt})

// 		// Create transfer invocation from a DIFFERENT provider than the allocation audience
// 		otherProvider := testutil.RandomSigner(t)
// 		transferCap := ucan.NewCapability(
// 			cmdreplica.TransferAbility,
// 			otherProvider.DID().String(),
// 			cmdreplica.TransferCaveats{
// 				Space: space.DID(),
// 				Blob:  blob,
// 				Site:  site,
// 				Cause: allocInv.Link(),
// 			},
// 		)
// 		// audience is otherProvider (different from storageProvider who is alloc audience)
// 		transferInv, err := invocation.Invoke(uploadService, otherProvider, transferCap)
// 		require.NoError(t, err)

// 		// receipt issued by otherProvider
// 		transferRcpt, err := receipt.Issue(
// 			otherProvider,
// 			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
// 			ran.FromInvocation(transferInv),
// 		)
// 		require.NoError(t, err)

// 		err = ch.Handler(ctx, transferInv, transferRcpt, nil)
// 		require.Error(t, err)
// 		require.Contains(t, err.Error(), "transfer executor does not match replica allocation audience")
// 	})

// }
