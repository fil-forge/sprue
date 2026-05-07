package handlers_test

import (
	"net/url"
	"testing"

	blobcap "github.com/fil-forge/go-libstoracha/capabilities/blob"
	httpcap "github.com/fil-forge/go-libstoracha/capabilities/http"
	"github.com/fil-forge/go-libstoracha/capabilities/types"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/go-ucanto/core/receipt/ran"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/ok"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/lib/didmailto"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	agent_store "github.com/fil-forge/sprue/pkg/store/agent/memory"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestHTTPPutConcludeHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("invalid http/put parameters", func(t *testing.T) {
		spStore := storage_provider_store.New()
		router := routing.NewService(spStore, logger)
		agentStore := agent_store.New()
		blobReg, _ := newBlobRegistry()
		nodeProvider := piriclient.NewProvider(uploadService, logger)

		ch := handlers.NewHTTPPutConcludeHandler(router, nodeProvider, agentStore, blobReg, logger)

		// Create an invocation with a wrong capability (not http/put)
		cap := ucan.NewCapability(
			"blob/allocate",
			uploadService.DID().String(),
			ucan.NoCaveats{},
		)
		putInv, err := invocation.Invoke(uploadService, uploadService, cap)
		require.NoError(t, err)

		putRcpt, err := receipt.Issue(
			uploadService,
			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
			ran.FromInvocation(putInv),
		)
		require.NoError(t, err)

		err = ch.Handler(ctx, putInv, putRcpt, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "matching http/put invocation")
	})

	t.Run("allocation invocation not found", func(t *testing.T) {
		spStore := storage_provider_store.New()
		router := routing.NewService(spStore, logger)
		agentStore := agent_store.New()
		blobReg, _ := newBlobRegistry()
		nodeProvider := piriclient.NewProvider(uploadService, logger)

		ch := handlers.NewHTTPPutConcludeHandler(router, nodeProvider, agentStore, blobReg, logger)

		digest := testutil.RandomMultihash(t)
		// URL.UcanAwait.Link points to a non-existent allocation invocation
		nonExistentAllocLink := cidlink.Link{Cid: testutil.RandomCID(t)}

		putInv, err := httpcap.Put.Invoke(
			uploadService, uploadService,
			uploadService.DID().String(),
			httpcap.PutCaveats{
				URL: types.Promise{
					UcanAwait: types.Await{
						Selector: ".out.ok.address.url",
						Link:     nonExistentAllocLink,
					},
				},
				Headers: types.Promise{
					UcanAwait: types.Await{
						Selector: ".out.ok.address.headers",
						Link:     nonExistentAllocLink,
					},
				},
				Body: httpcap.Body{
					Digest: digest,
					Size:   1024,
				},
			},
		)
		require.NoError(t, err)

		putRcpt, err := receipt.Issue(
			uploadService,
			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
			ran.FromInvocation(putInv),
		)
		require.NoError(t, err)

		err = ch.Handler(ctx, putInv, putRcpt, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "getting allocation invocation")
	})

	t.Run("storage provider not found", func(t *testing.T) {
		storageProvider := testutil.RandomSigner(t)
		spStore := storage_provider_store.New()
		router := routing.NewService(spStore, logger)
		agentStore := agent_store.New()
		blobReg, _ := newBlobRegistry()
		nodeProvider := piriclient.NewProvider(uploadService, logger)

		ch := handlers.NewHTTPPutConcludeHandler(router, nodeProvider, agentStore, blobReg, logger)

		space := testutil.RandomSigner(t)
		digest := testutil.RandomMultihash(t)
		blob := types.Blob{Digest: digest, Size: 1024}

		// Create and store a blob/allocate invocation
		allocInv, err := blobcap.Allocate.Invoke(
			uploadService, storageProvider,
			storageProvider.DID().String(),
			blobcap.AllocateCaveats{
				Space: space.DID(),
				Blob:  blob,
				Cause: cidlink.Link{Cid: testutil.RandomCID(t)},
			},
		)
		require.NoError(t, err)

		allocRcpt, err := receipt.Issue(
			storageProvider,
			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
			ran.FromInvocation(allocInv),
		)
		require.NoError(t, err)

		writeAgentMessage(t, agentStore, []invocation.Invocation{allocInv}, []receipt.AnyReceipt{allocRcpt})

		// Create http/put invocation referencing the allocation
		putInv, err := httpcap.Put.Invoke(
			uploadService, uploadService,
			uploadService.DID().String(),
			httpcap.PutCaveats{
				URL: types.Promise{
					UcanAwait: types.Await{
						Selector: ".out.ok.address.url",
						Link:     allocInv.Link(),
					},
				},
				Headers: types.Promise{
					UcanAwait: types.Await{
						Selector: ".out.ok.address.headers",
						Link:     allocInv.Link(),
					},
				},
				Body: httpcap.Body{
					Digest: digest,
					Size:   1024,
				},
			},
		)
		require.NoError(t, err)

		putRcpt, err := receipt.Issue(
			uploadService,
			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
			ran.FromInvocation(putInv),
		)
		require.NoError(t, err)

		// storageProvider is NOT registered in spStore, so GetProviderInfo fails
		err = ch.Handler(ctx, putInv, putRcpt, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "getting storage provider info")
	})

	t.Run("success registers blob in space", func(t *testing.T) {
		storageProvider := testutil.RandomSigner(t)
		storageProviderURL := testutil.Must(url.Parse("https://piri.example.com"))(t)
		storageProviderProof := delegateStorageProviderProof(t, storageProvider, uploadService)

		spStore := storage_provider_store.New()
		err := spStore.Put(ctx, *storageProviderURL, storageProviderProof, 100, nil)
		require.NoError(t, err)

		router := routing.NewService(spStore, logger)
		agentStore := agent_store.New()
		blobReg, consumerStore := newBlobRegistry()

		space := testutil.RandomSigner(t)
		digest := testutil.RandomMultihash(t)
		blob := types.Blob{Digest: digest, Size: 1024}

		// provision the space so blob registry Register succeeds
		aliceAccount := testutil.Must(didmailto.Parse("did:mailto:example.com:alice"))(t)
		err = consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "", testutil.RandomCID(t))
		require.NoError(t, err)

		// Create and store a blob/allocate invocation
		allocInv, err := blobcap.Allocate.Invoke(
			uploadService, storageProvider,
			storageProvider.DID().String(),
			blobcap.AllocateCaveats{
				Space: space.DID(),
				Blob:  blob,
				Cause: cidlink.Link{Cid: testutil.RandomCID(t)},
			},
		)
		require.NoError(t, err)

		allocRcpt, err := receipt.Issue(
			storageProvider,
			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
			ran.FromInvocation(allocInv),
		)
		require.NoError(t, err)

		writeAgentMessage(t, agentStore, []invocation.Invocation{allocInv}, []receipt.AnyReceipt{allocRcpt})

		// Create http/put invocation referencing the allocation
		putInv, err := httpcap.Put.Invoke(
			uploadService, uploadService,
			uploadService.DID().String(),
			httpcap.PutCaveats{
				URL: types.Promise{
					UcanAwait: types.Await{
						Selector: ".out.ok.address.url",
						Link:     allocInv.Link(),
					},
				},
				Headers: types.Promise{
					UcanAwait: types.Await{
						Selector: ".out.ok.address.headers",
						Link:     allocInv.Link(),
					},
				},
				Body: httpcap.Body{
					Digest: digest,
					Size:   1024,
				},
			},
		)
		require.NoError(t, err)

		putRcpt, err := receipt.Issue(
			uploadService,
			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
			ran.FromInvocation(putInv),
		)
		require.NoError(t, err)

		// Create mock node provider with accept handler that returns success
		acceptOk := blobcap.AcceptOk{
			Site: cidlink.Link{Cid: testutil.RandomCID(t)},
		}
		mockProvider := newMockNodeProvider(
			t,
			uploadService,
			storageProvider,
			newOkHandler[blobcap.AllocateCaveats](t, blobcap.AllocateOk{}),
			newOkHandler[blobcap.AcceptCaveats](t, acceptOk),
			logger,
		)

		ch := handlers.NewHTTPPutConcludeHandler(router, mockProvider, agentStore, blobReg, logger)

		err = ch.Handler(ctx, putInv, putRcpt, nil)
		require.NoError(t, err)
	})
}
