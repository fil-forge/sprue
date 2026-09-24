package handlers

import (
	"context"
	"net/url"
	"testing"

	"github.com/fil-forge/libforge/attestation/didmailto"
	blobcmds "github.com/fil-forge/libforge/commands/blob"
	httpcmds "github.com/fil-forge/libforge/commands/http"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/store/agent"
	agent_store "github.com/fil-forge/sprue/pkg/store/agent/memory"
	blob_registry "github.com/fil-forge/sprue/pkg/store/blob_registry/memory"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	metrics_store "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	routing_policy_store "github.com/fil-forge/sprue/pkg/store/routing_policy/memory"
	spacediff_store "github.com/fil-forge/sprue/pkg/store/space_diff/memory"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/promise"
	"github.com/fil-forge/ucantone/ucan/receipt"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

type httpPutDeps struct {
	ch            ConclusionHandler
	spStore       *storage_provider_store.Store
	agentStore    *agent_store.Store
	consumerStore *consumer_store.Store
	blobReg       *blob_registry.Store
}

func newHTTPPutDeps(t *testing.T, nodeProvider piriclient.Provider, logger *zap.Logger) *httpPutDeps {
	t.Helper()
	spStore := storage_provider_store.New()
	router := routing.NewService(spStore, routing_policy_store.New(), logger)
	agentStore := agent_store.New()
	consumerStore := consumer_store.New()
	blobReg := blob_registry.New(
		spacediff_store.New(),
		metrics_store.NewSpaceStore(),
		metrics_store.New(),
	)
	ch := NewHTTPPutConcludeHandler(router, nodeProvider, agentStore, blobReg, logger)
	return &httpPutDeps{
		ch:            ch,
		spStore:       spStore,
		agentStore:    agentStore,
		consumerStore: consumerStore,
		blobReg:       blobReg,
	}
}

func TestHTTPPutConcludeHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("allocation invocation not found", func(t *testing.T) {
		deps := newHTTPPutDeps(t, piriclient.NewProvider(uploadService, logger), logger)

		digest := testutil.RandomMultihash(t)
		// Destination.Task points to an invocation that's not in the agent store.
		nonExistentAllocTask := testutil.RandomCID(t)

		blobProvider := testutil.DeriveBlobProvider(t, digest)
		putInv, err := httpcmds.Put.Invoke(
			blobProvider,
			blobProvider.DID(),
			&httpcmds.PutArguments{
				Body:        blobcmds.Blob{Digest: digest, Size: 1024},
				Destination: promise.AwaitOK{Task: nonExistentAllocTask},
			},
			invocation.WithAudience(blobProvider.DID()),
		)
		require.NoError(t, err)

		putRcpt, err := receipt.IssueOK(
			blobProvider,
			putInv.Task().Link(),
			&httpcmds.PutOK{},
		)
		require.NoError(t, err)

		_, err = deps.ch.Handler(ctx, []Conclusion{{Invocation: putInv, Receipt: putRcpt}})
		require.Error(t, err)
		require.Contains(t, err.Error(), "getting allocation invocation")
	})

	t.Run("storage provider not found", func(t *testing.T) {
		deps := newHTTPPutDeps(t, piriclient.NewProvider(uploadService, logger), logger)

		storageProvider := testutil.RandomIssuer(t)
		space := testutil.RandomIssuer(t)
		digest := testutil.RandomMultihash(t)
		blob := blobcmds.Blob{Digest: digest, Size: 1024}

		// Persist a /blob/allocate invocation for the storage provider, but do
		// NOT register that provider in the spStore — router lookup fails.
		allocInv, err := blobcmds.Allocate.Invoke(
			uploadService,
			storageProvider.DID(),
			&blobcmds.AllocateArguments{Space: space.DID(), Blob: blob, Cause: testutil.RandomCID(t)},
			invocation.WithAudience(storageProvider.DID()),
		)
		require.NoError(t, err)
		allocRcpt, err := receipt.IssueOK(
			storageProvider,
			allocInv.Task().Link(),
			&blobcmds.AllocateOK{Size: blob.Size},
		)
		require.NoError(t, err)
		msg := container.New(
			container.WithInvocations(allocInv),
			container.WithReceipts(allocRcpt),
		)
		require.NoError(t, deps.agentStore.Write(ctx, msg, agent.Index(msg)))

		blobProvider := testutil.DeriveBlobProvider(t, digest)
		putInv, err := httpcmds.Put.Invoke(
			blobProvider,
			blobProvider.DID(),
			&httpcmds.PutArguments{
				Body:        blob,
				Destination: promise.AwaitOK{Task: allocInv.Task().Link()},
			},
			invocation.WithAudience(blobProvider.DID()),
		)
		require.NoError(t, err)
		putRcpt, err := receipt.IssueOK(
			blobProvider,
			putInv.Task().Link(),
			&httpcmds.PutOK{},
		)
		require.NoError(t, err)

		_, err = deps.ch.Handler(ctx, []Conclusion{{Invocation: putInv, Receipt: putRcpt}})
		require.Error(t, err)
		require.Contains(t, err.Error(), "getting storage provider info")
	})

	t.Run("success registers blob in space", func(t *testing.T) {
		storageProvider := testutil.RandomIssuer(t)
		space := testutil.RandomIssuer(t)
		digest := testutil.RandomMultihash(t)
		blob := blobcmds.Blob{Digest: digest, Size: 1024}
		blobAddTaskLink := testutil.RandomCID(t)

		// Stand up a mock piri server. The handler under test only calls
		// /blob/accept; the allocate handler is irrelevant but the helper
		// requires both.
		acceptOK := &blobcmds.AcceptOK{
			Site: testutil.RandomCID(t),
			PDP:  promise.AwaitOK{Task: testutil.RandomCID(t)},
		}
		piriSrv := testutil.NewMockPiriServer(
			t, storageProvider, uploadService,
			&blobcmds.AllocateOK{Size: blob.Size},
			acceptOK,
		)
		piriURL := testutil.Must(url.Parse(piriSrv.URL))(t)

		deps := newHTTPPutDeps(t, piriclient.NewProvider(uploadService, logger), logger)
		require.NoError(t, deps.spStore.Put(ctx, storageProvider.DID(), *piriURL, 100, nil, testutil.ProviderProofs(t, storageProvider, uploadService)))

		// Provision the space so blob_registry.Register succeeds.
		account := testutil.Must(didmailto.New("alice@example.com"))(t)
		require.NoError(t, deps.consumerStore.Add(
			ctx, uploadService.DID(), space.DID(), account, "sub-1", testutil.RandomCID(t),
		))

		// Prior /blob/allocate invocation in the agent store.
		allocInv, err := blobcmds.Allocate.Invoke(
			uploadService,
			storageProvider.DID(),
			&blobcmds.AllocateArguments{Space: space.DID(), Blob: blob, Cause: blobAddTaskLink},
			invocation.WithAudience(storageProvider.DID()),
		)
		require.NoError(t, err)
		allocRcpt, err := receipt.IssueOK(
			storageProvider,
			allocInv.Task().Link(),
			&blobcmds.AllocateOK{Size: blob.Size},
		)
		require.NoError(t, err)
		msg := container.New(
			container.WithInvocations(allocInv),
			container.WithReceipts(allocRcpt),
		)
		require.NoError(t, deps.agentStore.Write(ctx, msg, agent.Index(msg)))

		// /http/put invocation referring to the allocation task.
		blobProvider := testutil.DeriveBlobProvider(t, digest)
		putInv, err := httpcmds.Put.Invoke(
			blobProvider,
			blobProvider.DID(),
			&httpcmds.PutArguments{
				Body:        blob,
				Destination: promise.AwaitOK{Task: allocInv.Task().Link()},
			},
			invocation.WithAudience(blobProvider.DID()),
		)
		require.NoError(t, err)
		putRcpt, err := receipt.IssueOK(
			blobProvider,
			putInv.Task().Link(),
			&httpcmds.PutOK{},
		)
		require.NoError(t, err)

		// The upload service is authorized to invoke /blob/accept by the proofs
		// the provider granted it at registration (sourced from the provider
		// record), so no proof travels in the conclude metadata.
		meta, err := deps.ch.Handler(ctx, []Conclusion{{Invocation: putInv, Receipt: putRcpt}})
		require.NoError(t, err)

		// The accept receipt travels back in the conclude response, so the
		// deliverer reads the outcome instead of polling for it.
		require.Len(t, meta.Receipts(), 1)
		acceptedOK, err := blobcmds.Accept.Unpack(meta.Receipts()[0])
		require.NoError(t, err)
		require.Equal(t, acceptOK.Site, acceptedOK.Site)

		// Blob should now be registered in the space, with cause = blobAddTaskLink.
		rec, err := deps.blobReg.Get(ctx, space.DID(), digest)
		require.NoError(t, err)
		require.Equal(t, blobAddTaskLink, rec.Cause)
		require.Equal(t, blob.Size, rec.Blob.Size)
	})
}

// countingAgentStore counts how the completion path reads the agent store.
type countingAgentStore struct {
	agent.Store
	single, batch int
}

func (c *countingAgentStore) GetInvocation(ctx context.Context, task cid.Cid) (ucan.Invocation, error) {
	c.single++
	return c.Store.GetInvocation(ctx, task)
}

func (c *countingAgentStore) GetInvocations(ctx context.Context, tasks []cid.Cid) (map[cid.Cid]ucan.Invocation, error) {
	c.batch++
	return c.Store.GetInvocations(ctx, tasks)
}

// TestResolveAllocationsLooksUpOnce pins that a batch of concluded puts costs
// one agent-store lookup for its allocations rather than one per blob, and
// that the allocations come back in delivery order.
func TestResolveAllocationsLooksUpOnce(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()
	uploadService := testutil.WebService
	storageProvider := testutil.RandomIssuer(t)
	deps := newHTTPPutDeps(t, piriclient.NewProvider(uploadService, logger), logger)

	parked := parkBlobs(t, ctx, deps, uploadService, storageProvider, testutil.RandomIssuer(t).DID(), testutil.RandomCID(t), 5)
	conclusions := make([]Conclusion, len(parked))
	for i, p := range parked {
		conclusions[i] = p.conc
	}

	counting := &countingAgentStore{Store: deps.agentStore}
	puts, err := resolveAllocations(ctx, counting, conclusions, logger)
	require.NoError(t, err)
	require.Equal(t, 1, counting.batch, "one lookup for the whole batch")
	require.Zero(t, counting.single, "no per-blob lookups")
	require.Len(t, puts, len(parked))
	for i, put := range puts {
		require.Equal(t, parked[i].digest.String(), put.blob.Digest.String(), "put %d resolved to another blob's allocation", i)
		require.Equal(t, storageProvider.DID(), put.provider)
	}
}
