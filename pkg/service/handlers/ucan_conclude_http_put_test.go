package handlers_test

import (
	"net/url"
	"testing"

	"github.com/fil-forge/libforge/attestation/didmailto"
	blobcmds "github.com/fil-forge/libforge/commands/blob"
	httpcmds "github.com/fil-forge/libforge/commands/http"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	"github.com/fil-forge/sprue/pkg/store/agent"
	agent_store "github.com/fil-forge/sprue/pkg/store/agent/memory"
	blob_registry "github.com/fil-forge/sprue/pkg/store/blob_registry/memory"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	metrics_store "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	spacediff_store "github.com/fil-forge/sprue/pkg/store/space_diff/memory"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/promise"
	"github.com/fil-forge/ucantone/ucan/receipt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

type httpPutDeps struct {
	ch            handlers.ConclusionHandler
	spStore       *storage_provider_store.Store
	agentStore    *agent_store.Store
	consumerStore *consumer_store.Store
	blobReg       *blob_registry.Store
}

func newHTTPPutDeps(t *testing.T, nodeProvider piriclient.Provider, logger *zap.Logger) *httpPutDeps {
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
	ch := handlers.NewHTTPPutConcludeHandler(router, nodeProvider, agentStore, blobReg, logger)
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

		blobProvider := deriveBlobProvider(t, digest)
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

		err = deps.ch.Handler(ctx, putInv, putRcpt)
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

		blobProvider := deriveBlobProvider(t, digest)
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

		err = deps.ch.Handler(ctx, putInv, putRcpt)
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
		piriSrv := newMockPiriServer(
			t, storageProvider, uploadService,
			&blobcmds.AllocateOK{Size: blob.Size},
			acceptOK,
		)
		piriURL := testutil.Must(url.Parse(piriSrv.URL))(t)

		deps := newHTTPPutDeps(t, piriclient.NewProvider(uploadService, logger), logger)
		require.NoError(t, deps.spStore.Put(ctx, storageProvider.DID(), *piriURL, 100, nil, providerProofs(t, storageProvider, uploadService)))

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
		blobProvider := deriveBlobProvider(t, digest)
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
		err = deps.ch.Handler(ctx, putInv, putRcpt)
		require.NoError(t, err)

		// Blob should now be registered in the space, with cause = blobAddTaskLink.
		rec, err := deps.blobReg.Get(ctx, space.DID(), digest)
		require.NoError(t, err)
		require.Equal(t, blobAddTaskLink, rec.Cause)
		require.Equal(t, blob.Size, rec.Blob.Size)
	})
}
