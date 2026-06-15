package handlers_test

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/fil-forge/libforge/commands"
	accesscmds "github.com/fil-forge/libforge/commands/access"
	blobcmds "github.com/fil-forge/libforge/commands/blob"
	httpcmds "github.com/fil-forge/libforge/commands/http"
	"github.com/fil-forge/libforge/didmailto"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	"github.com/fil-forge/sprue/pkg/store/agent"
	agent_store "github.com/fil-forge/sprue/pkg/store/agent/memory"
	blob_registry "github.com/fil-forge/sprue/pkg/store/blob_registry/memory"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	metrics_store "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	spacediff_store "github.com/fil-forge/sprue/pkg/store/space_diff/memory"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	subscription_store "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/principal"
	ed25519signer "github.com/fil-forge/ucantone/principal/ed25519"
	"github.com/fil-forge/ucantone/principal/verifier"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/promise"
	"github.com/fil-forge/ucantone/ucan/receipt"
	"github.com/fil-forge/ucantone/validator"
	"github.com/fil-forge/ucantone/validator/errors"
	"github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

type blobAddTestDeps struct {
	handler           server.Route
	consumerStore     *consumer_store.Store
	subscriptionStore *subscription_store.Store
	spStore           *storage_provider_store.Store
	agentStore        *agent_store.Store
	blobReg           *blob_registry.Store
}

func newBlobAddTestDeps(t *testing.T, uploadService principal.Signer, logger *zap.Logger) *blobAddTestDeps {
	t.Helper()
	consumerStore := consumer_store.New()
	subscriptionStore := subscription_store.New()
	provisioningSvc := provisioning.NewService([]did.DID{uploadService.DID()}, consumerStore, subscriptionStore)
	spStore := storage_provider_store.New()
	router := routing.NewService(spStore, logger)
	agentStore := agent_store.New()
	blobReg := blob_registry.New(
		spacediff_store.New(),
		consumerStore,
		metrics_store.NewSpaceStore(),
		metrics_store.New(),
	)
	nodeProvider := piriclient.NewProvider(uploadService, logger)
	handler := handlers.NewBlobAddHandler(
		&identity.Identity{Signer: uploadService},
		provisioningSvc,
		router,
		nodeProvider,
		agentStore,
		blobReg,
		logger,
	)
	return &blobAddTestDeps{
		handler:           handler,
		consumerStore:     consumerStore,
		subscriptionStore: subscriptionStore,
		spStore:           spStore,
		agentStore:        agentStore,
		blobReg:           blobReg,
	}
}

// provisionSpace adds the consumer record so provisioningSvc.ListServiceProviders
// returns the upload service for the space.
func provisionSpace(t *testing.T, deps *blobAddTestDeps, uploadService principal.Signer, space did.DID) {
	t.Helper()
	account := testutil.Must(didmailto.New("alice@example.com"))(t)
	err := deps.consumerStore.Add(
		context.Background(),
		uploadService.DID(),
		space,
		account,
		"sub-1",
		testutil.RandomCID(t),
	)
	require.NoError(t, err)
}

// newMockPiriServer stands up a UCAN HTTP server that handles /blob/allocate &
// /blob/accept by returning the canned responses. Wraps the upload service's
// did:web identity so signatures verify against the underlying did:key.
func newMockPiriServer(
	t *testing.T,
	storageProvider principal.Signer,
	uploadService principal.Signer,
	allocateOK *blobcmds.AllocateOK,
	acceptOK *blobcmds.AcceptOK,
) *httptest.Server {
	t.Helper()

	resolveDIDKey := func(ctx context.Context, d did.DID) (ucan.Verifier, error) {
		if d == uploadService.DID() {
			return uploadService.Verifier(), nil
		}
		if d.Method() == "key" {
			return verifier.FromDIDKey(d)
		}
		return nil, errors.NewDIDKeyResolutionError(d, fmt.Errorf("unexpected DID to resolve"))
	}

	srv := server.NewHTTP(
		storageProvider,
		server.WithValidationOptions(validator.WithDIDVerifierResolver(resolveDIDKey)),
	)

	srv.Handle(blobcmds.Allocate.Command, blobcmds.Allocate.Handler(func(
		req *binding.Request[*blobcmds.AllocateArguments],
		res *binding.Response[*blobcmds.AllocateOK],
	) error {
		return res.SetSuccess(allocateOK)
	}))

	srv.Handle(blobcmds.Accept.Command, blobcmds.Accept.Handler(func(
		req *binding.Request[*blobcmds.AcceptArguments],
		res *binding.Response[*blobcmds.AcceptOK],
	) error {
		return res.SetSuccess(acceptOK)
	}))

	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)
	return httpSrv
}

// providerProofs builds the proof container a storage provider grants the
// upload service at registration: self-issued delegations (subject = provider)
// authorizing `/blob/allocate` and `/blob/accept`.
func providerProofs(t *testing.T, storageProvider, uploadService principal.Signer) ucan.Container {
	t.Helper()
	allocProof := testutil.Must(blobcmds.Allocate.Delegate(storageProvider, uploadService.DID(), storageProvider.DID()))(t)
	acceptProof := testutil.Must(blobcmds.Accept.Delegate(storageProvider, uploadService.DID(), storageProvider.DID()))(t)
	return container.New(container.WithDelegations(allocProof, acceptProof))
}

func TestBlobAddHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("no providers for space", func(t *testing.T) {
		deps := newBlobAddTestDeps(t, uploadService, logger)

		space := testutil.RandomSigner(t)
		args := blobcmds.AddArguments{
			Blob: blobcmds.Blob{Digest: testutil.RandomMultihash(t), Size: 1024},
		}

		inv, err := blobcmds.Add.Invoke(
			testutil.Alice,
			space.DID(),
			&args,
			invocation.WithAudience(uploadService.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = deps.handler.Handler(req, res)
		require.NoError(t, err)

		_, err = blobcmds.Add.Unpack(res.Receipt())
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		require.Equal(t, accesscmds.InsufficientStorageErrorName, errModel.Name())
	})

	t.Run("no candidates available", func(t *testing.T) {
		deps := newBlobAddTestDeps(t, uploadService, logger)

		space := testutil.RandomSigner(t)
		provisionSpace(t, deps, uploadService, space.DID())

		// No storage providers in spStore — the router will return ErrCandidateUnavailable.
		args := blobcmds.AddArguments{
			Blob: blobcmds.Blob{Digest: testutil.RandomMultihash(t), Size: 1024},
		}

		inv, err := blobcmds.Add.Invoke(
			testutil.Alice,
			space.DID(),
			&args,
			invocation.WithAudience(uploadService.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = deps.handler.Handler(req, res)
		require.NoError(t, err)

		_, err = blobcmds.Add.Unpack(res.Receipt())
		require.ErrorIs(t, err, routing.ErrCandidateUnavailable)
	})

	t.Run("zero weight providers returns candidate unavailable", func(t *testing.T) {
		deps := newBlobAddTestDeps(t, uploadService, logger)

		space := testutil.RandomSigner(t)
		provisionSpace(t, deps, uploadService, space.DID())

		// Register a storage provider with weight 0 — it'll be filtered out.
		storageProvider := testutil.RandomSigner(t)
		endpoint := testutil.Must(url.Parse("https://piri.example.com"))(t)
		err := deps.spStore.Put(ctx, storageProvider.DID(), *endpoint, 0, nil, container.New())
		require.NoError(t, err)

		args := blobcmds.AddArguments{
			Blob: blobcmds.Blob{Digest: testutil.RandomMultihash(t), Size: 1024},
		}

		inv, err := blobcmds.Add.Invoke(
			testutil.Alice,
			space.DID(),
			&args,
			invocation.WithAudience(uploadService.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = deps.handler.Handler(req, res)
		require.NoError(t, err)

		_, err = blobcmds.Add.Unpack(res.Receipt())
		require.ErrorIs(t, err, routing.ErrCandidateUnavailable)
	})

	t.Run("successful allocation with address", func(t *testing.T) {
		deps := newBlobAddTestDeps(t, uploadService, logger)

		space := testutil.RandomSigner(t)
		provisionSpace(t, deps, uploadService, space.DID())

		storageProvider := testutil.RandomSigner(t)
		putURL := testutil.Must(url.Parse("https://storage.example.com/put"))(t)
		allocateOK := &blobcmds.AllocateOK{
			Size: 1024,
			Address: &blobcmds.BlobAddress{
				URL:     commands.CborURL(*putURL),
				Headers: map[string]string{},
				Expires: time.Now().Add(time.Hour).Unix(),
			},
		}
		// Accept handler is registered but should not be invoked when an Address is
		// returned — the put receipt isn't issued, so maybeAccept skips Accept.
		acceptOK := &blobcmds.AcceptOK{
			Site: testutil.RandomCID(t),
			PDP:  promise.AwaitOK{Task: testutil.RandomCID(t)},
		}

		piriSrv := newMockPiriServer(t, storageProvider, uploadService, allocateOK, acceptOK)
		piriURL := testutil.Must(url.Parse(piriSrv.URL))(t)

		// The upload service is authorized to invoke /blob/allocate and /blob/accept
		// by the proofs the provider granted it at registration, sourced from the
		// provider record rather than the invocation metadata.
		err := deps.spStore.Put(ctx, storageProvider.DID(), *piriURL, 100, nil, providerProofs(t, storageProvider, uploadService))
		require.NoError(t, err)

		args := blobcmds.AddArguments{
			Blob: blobcmds.Blob{Digest: testutil.RandomMultihash(t), Size: 1024},
		}

		inv, err := blobcmds.Add.Invoke(
			testutil.Alice,
			space.DID(),
			&args,
			invocation.WithAudience(uploadService.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = deps.handler.Handler(req, res)
		require.NoError(t, err)

		_, err = blobcmds.Allocate.Unpack(res.Receipt())
		require.NoError(t, err)

		// Response metadata should carry the allocate, put, and accept invocations.
		require.NotNil(t, res.Metadata())
		require.NotEmpty(t, res.Metadata().Invocations())
	})

	t.Run("successful allocation blob already stored", func(t *testing.T) {
		deps := newBlobAddTestDeps(t, uploadService, logger)

		space := testutil.RandomSigner(t)
		provisionSpace(t, deps, uploadService, space.DID())

		storageProvider := testutil.RandomSigner(t)
		// No address signals the blob is already on the provider — the handler
		// then issues the put receipt itself and proceeds to Accept on piri.
		allocateOK := &blobcmds.AllocateOK{Size: 1024, Address: nil}
		acceptOK := &blobcmds.AcceptOK{
			Site: testutil.RandomCID(t),
			PDP:  promise.AwaitOK{Task: testutil.RandomCID(t)},
		}

		piriSrv := newMockPiriServer(t, storageProvider, uploadService, allocateOK, acceptOK)
		piriURL := testutil.Must(url.Parse(piriSrv.URL))(t)

		err := deps.spStore.Put(ctx, storageProvider.DID(), *piriURL, 100, nil, providerProofs(t, storageProvider, uploadService))
		require.NoError(t, err)

		args := blobcmds.AddArguments{
			Blob: blobcmds.Blob{Digest: testutil.RandomMultihash(t), Size: 1024},
		}

		inv, err := blobcmds.Add.Invoke(
			testutil.Alice,
			space.DID(),
			&args,
			invocation.WithAudience(uploadService.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = deps.handler.Handler(req, res)
		require.NoError(t, err)

		_, err = blobcmds.Allocate.Unpack(res.Receipt())
		require.NoError(t, err)

		// Both invocations and receipts should be in the metadata since accept ran.
		require.NotNil(t, res.Metadata())
		require.NotEmpty(t, res.Metadata().Invocations())
		require.NotEmpty(t, res.Metadata().Receipts())
	})

	t.Run("blob already registered in space", func(t *testing.T) {
		deps := newBlobAddTestDeps(t, uploadService, logger)

		space := testutil.RandomSigner(t)
		provisionSpace(t, deps, uploadService, space.DID())

		storageProvider := testutil.RandomSigner(t)
		digest := testutil.RandomMultihash(t)
		blob := blobcmds.Blob{Digest: digest, Size: 1024}

		// Build the chain that the handler will walk back through:
		//   addRcpt → accInv/accRcpt → putInv/putRcpt → allocInv/allocRcpt
		blobProvider := deriveBlobProvider(t, digest)

		// /blob/allocate
		allocInv := testutil.Must(blobcmds.Allocate.Invoke(
			uploadService,
			space.DID(),
			&blobcmds.AllocateArguments{Blob: blob, Cause: testutil.RandomCID(t)},
			invocation.WithAudience(storageProvider.DID()),
		))(t)
		allocRcpt := testutil.Must(receipt.IssueOK(
			storageProvider,
			allocInv.Task().Link(),
			&blobcmds.AllocateOK{Size: blob.Size},
		))(t)

		// /http/put — issued by the principal derived from the blob digest.
		putInv := testutil.Must(httpcmds.Put.Invoke(
			blobProvider,
			blobProvider.DID(),
			&httpcmds.PutArguments{
				Body:        blob,
				Destination: promise.AwaitOK{Task: allocInv.Task().Link()},
			},
			invocation.WithAudience(blobProvider.DID()),
		))(t)
		putRcpt := testutil.Must(receipt.IssueOK(
			blobProvider,
			putInv.Task().Link(),
			&httpcmds.PutOK{},
		))(t)

		// /blob/accept
		accInv := testutil.Must(blobcmds.Accept.Invoke(
			uploadService,
			space.DID(),
			&blobcmds.AcceptArguments{
				Blob: blob,
				Put:  promise.AwaitOK{Task: putInv.Task().Link()},
			},
			invocation.WithAudience(storageProvider.DID()),
		))(t)
		accRcpt := testutil.Must(receipt.IssueOK(
			storageProvider,
			accInv.Task().Link(),
			&blobcmds.AcceptOK{
				Site: testutil.RandomCID(t),
				PDP:  promise.AwaitOK{Task: testutil.RandomCID(t)},
			},
		))(t)

		// The original /space/blob/add invocation and receipt — its receipt's
		// task CID is what gets stored in the registry as the cause.
		prevAddInv := testutil.Must(blobcmds.Add.Invoke(
			testutil.Alice,
			space.DID(),
			&blobcmds.AddArguments{Blob: blob},
			invocation.WithAudience(uploadService.DID()),
		))(t)
		prevAddRcpt := testutil.Must(receipt.IssueOK(
			uploadService,
			prevAddInv.Task().Link(),
			&blobcmds.AddOK{
				Site: promise.AwaitOK{Task: accInv.Task().Link()},
			},
		))(t)

		// Persist the chain in the agent store and register the blob with cause
		// pointing at the prior /blob/add invocation's task CID.
		msg := container.New(
			container.WithInvocations(allocInv, putInv, accInv, prevAddInv),
			container.WithReceipts(allocRcpt, putRcpt, accRcpt, prevAddRcpt),
		)
		require.NoError(t, deps.agentStore.Write(ctx, msg, agent.Index(msg)))
		require.NoError(t, deps.blobReg.Register(ctx, space.DID(), blob, prevAddInv.Task().Link()))

		// Re-invoke /blob/add for the same blob/space — the handler should hit
		// the already-registered short-circuit, walk the chain, and return the
		// stored AddOK without contacting any storage provider.
		inv := testutil.Must(blobcmds.Add.Invoke(
			testutil.Alice,
			space.DID(),
			&blobcmds.AddArguments{Blob: blob},
			invocation.WithAudience(uploadService.DID()),
		))(t)

		req := execution.NewRequest(ctx, inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = deps.handler.Handler(req, res)
		require.NoError(t, err)

		gotAddOK, err := blobcmds.Add.Unpack(res.Receipt())
		require.NoError(t, err)

		// The returned AddOK should match the one from the prior receipt.
		require.Equal(t, accInv.Task().Link(), gotAddOK.Site.Task)

		// Response metadata should carry all three invocations and all three
		// receipts from the prior chain.
		require.NotNil(t, res.Metadata())
		require.Len(t, res.Metadata().Invocations(), 3)
		require.Len(t, res.Metadata().Receipts(), 3)
	})
}

// deriveBlobProvider mirrors the production handler's logic for deriving a
// signer from a blob's digest, used to sign /http/put invocations and receipts.
func deriveBlobProvider(t *testing.T, digest multihash.Multihash) principal.Signer {
	t.Helper()
	require.GreaterOrEqual(t, len(digest), ed25519.SeedSize)
	seed := digest[len(digest)-ed25519.SeedSize:]
	s, err := ed25519signer.FromRaw(seed)
	require.NoError(t, err)
	return s
}
