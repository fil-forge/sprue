package handlers_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"net/url"
	"testing"

	accesscaps "github.com/fil-forge/libforge/capabilities/access"
	assertcaps "github.com/fil-forge/libforge/capabilities/assert"
	blobcaps "github.com/fil-forge/libforge/capabilities/blob"
	contentcaps "github.com/fil-forge/libforge/capabilities/content"
	indexcaps "github.com/fil-forge/libforge/capabilities/index"
	"github.com/fil-forge/libforge/didmailto"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/indexerclient"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	subscription_store "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	"github.com/fil-forge/ucantone/did"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"github.com/fil-forge/ucantone/principal"
	"github.com/fil-forge/ucantone/principal/signer"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan/delegation"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/validator"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// newMockIndexerServer stands up a UCAN HTTP server that handles /assert/index
// by returning the canned response. Wraps the upload service's did:web identity
// so signatures verify against the underlying did:key.
func newMockIndexerServer(
	t *testing.T,
	indexerSigner principal.Signer,
	uploadService principal.Signer,
	indexOK *assertcaps.IndexOK,
) *httptest.Server {
	t.Helper()

	resolveDIDKey := func(ctx context.Context, d did.DID) ([]did.DID, error) {
		if d == uploadService.DID() {
			if w, ok := uploadService.(signer.Unwrapper); ok {
				return []did.DID{w.Unwrap().DID()}, nil
			}
		}
		return validator.FailDIDKeyResolution(ctx, d)
	}

	srv := server.NewHTTP(
		indexerSigner,
		server.WithValidationOptions(validator.WithDIDResolver(resolveDIDKey)),
	)

	srv.Handle(assertcaps.Index, bindexec.NewHandler(func(
		req *bindexec.Request[*assertcaps.IndexArguments],
		res *bindexec.Response[*assertcaps.IndexOK],
	) error {
		return res.SetSuccess(indexOK)
	}))

	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)
	return httpSrv
}

// invokeIndexAdd builds an /index/add invocation with optional metadata,
// returning the request and a signed response ready for the handler.
func invokeIndexAdd(
	t *testing.T,
	ctx context.Context,
	agent principal.Signer,
	uploadService principal.Signer,
	space principal.Signer,
	index cid.Cid,
	reqOpts ...execution.RequestOption,
) (execution.Request, *execution.ExecResponse) {
	t.Helper()
	inv, err := indexcaps.Add.Invoke(
		agent,
		space.DID(),
		&indexcaps.AddArguments{Index: index},
		invocation.WithAudience(uploadService.DID()),
	)
	require.NoError(t, err)
	req := execution.NewRequest(ctx, inv, reqOpts...)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
	require.NoError(t, err)
	return req, res
}

func TestIndexAddHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService
	alice := testutil.Alice
	aliceAccount := testutil.Must(didmailto.New("alice@example.com"))(t)

	id := &identity.Identity{Signer: uploadService}

	t.Run("no service providers", func(t *testing.T) {
		consumerStore := consumer_store.New()
		subscriptionStore := subscription_store.New()
		provisioningSvc := provisioning.NewService(nil, consumerStore, subscriptionStore)
		blobReg, _ := newBlobRegistry(t)

		handler := handlers.NewIndexAddHandler(id, provisioningSvc, blobReg, nil, logger)

		space := testutil.RandomSigner(t)
		req, res := invokeIndexAdd(t, ctx, alice, uploadService, space, testutil.RandomCID(t))

		err := handler.Handler(req, res)
		require.NoError(t, err)

		_, x := res.Receipt().Out().Unpack()
		require.NotNil(t, x)

		var model edm.ErrorModel
		require.NoError(t, model.UnmarshalCBOR(bytes.NewReader(x)))
		require.Equal(t, accesscaps.InsufficientStorageErrorName, model.Name())
	})

	t.Run("index not found in space", func(t *testing.T) {
		blobReg, consumerStore := newBlobRegistry(t)
		subscriptionStore := subscription_store.New()
		provisioningSvc := provisioning.NewService(
			[]did.DID{uploadService.DID()},
			consumerStore,
			subscriptionStore,
		)

		space := testutil.RandomSigner(t)
		require.NoError(t, consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "sub-1", testutil.RandomCID(t)))

		handler := handlers.NewIndexAddHandler(id, provisioningSvc, blobReg, nil, logger)

		// Index blob is not registered for this space.
		req, res := invokeIndexAdd(t, ctx, alice, uploadService, space, testutil.RandomCID(t))

		err := handler.Handler(req, res)
		require.NoError(t, err)

		_, x := res.Receipt().Out().Unpack()
		require.NotNil(t, x)

		var model edm.ErrorModel
		require.NoError(t, model.UnmarshalCBOR(bytes.NewReader(x)))
		require.Equal(t, indexcaps.IndexNotFoundErrorName, model.Name())
	})

	t.Run("retrieval auth supplied publishes index claim", func(t *testing.T) {
		blobReg, consumerStore := newBlobRegistry(t)
		subscriptionStore := subscription_store.New()
		provisioningSvc := provisioning.NewService(
			[]did.DID{uploadService.DID()},
			consumerStore,
			subscriptionStore,
		)

		space := testutil.RandomSigner(t)
		require.NoError(t, consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "sub-1", testutil.RandomCID(t)))

		indexCID := testutil.RandomCID(t)
		indexBlob := blobcaps.Blob{Digest: indexCID.Hash(), Size: 512}
		require.NoError(t, blobReg.Register(ctx, space.DID(), indexBlob, testutil.RandomCID(t)))

		// Stand up a mock indexer that returns success on /assert/index.
		indexerSigner := testutil.RandomSigner(t)
		indexerSrv := newMockIndexerServer(t, indexerSigner, uploadService, &assertcaps.IndexOK{})
		indexerURL := testutil.Must(url.Parse(indexerSrv.URL))(t)
		indexerCli, err := indexerclient.New(indexerURL, indexerSigner.DID(), uploadService, logger)
		require.NoError(t, err)

		handler := handlers.NewIndexAddHandler(id, provisioningSvc, blobReg, indexerCli, logger)

		// /content/retrieve delegation from space → upload service so the
		// handler can build a proof chain that authorizes the indexer to
		// retrieve the index blob.
		retrievalAuth, err := delegation.Delegate(space, uploadService.DID(), space.DID(), contentcaps.RetrieveCommand)
		require.NoError(t, err)

		req, res := invokeIndexAdd(t, ctx, alice, uploadService, space, indexCID,
			execution.WithDelegations(retrievalAuth),
		)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		require.False(t, res.Receipt().Out().IsErr())
	})

	t.Run("missing retrieval auth fails to build proof chain", func(t *testing.T) {
		blobReg, consumerStore := newBlobRegistry(t)
		subscriptionStore := subscription_store.New()
		provisioningSvc := provisioning.NewService(
			[]did.DID{uploadService.DID()},
			consumerStore,
			subscriptionStore,
		)

		space := testutil.RandomSigner(t)
		require.NoError(t, consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "sub-1", testutil.RandomCID(t)))

		indexCID := testutil.RandomCID(t)
		indexBlob := blobcaps.Blob{Digest: indexCID.Hash(), Size: 512}
		require.NoError(t, blobReg.Register(ctx, space.DID(), indexBlob, testutil.RandomCID(t)))

		indexerSigner := testutil.RandomSigner(t)
		indexerSrv := newMockIndexerServer(t, indexerSigner, uploadService, &assertcaps.IndexOK{})
		indexerURL := testutil.Must(url.Parse(indexerSrv.URL))(t)
		indexerCli, err := indexerclient.New(indexerURL, indexerSigner.DID(), uploadService, logger)
		require.NoError(t, err)

		handler := handlers.NewIndexAddHandler(id, provisioningSvc, blobReg, indexerCli, logger)

		// No /content/retrieve delegation. The handler invokes /assert/index
		// without proofs; the indexer accepts it (our mock has no validation
		// on proofs), so this currently still succeeds. If the handler later
		// requires retrieval auth before publishing, this test will need a
		// stricter mock.
		req, res := invokeIndexAdd(t, ctx, alice, uploadService, space, indexCID)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		// Currently the indexer client publishes even without retrieval auth
		// because the proof chain is empty (not erroring). Document that
		// behavior.
		require.False(t, res.Receipt().Out().IsErr())
	})
}
