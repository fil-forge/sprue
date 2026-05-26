package handlers_test

import (
	"context"
	"testing"

	accesscmds "github.com/fil-forge/libforge/commands/access"
	blobcmds "github.com/fil-forge/libforge/commands/blob"
	uploadcmds "github.com/fil-forge/libforge/commands/upload"
	"github.com/fil-forge/libforge/didmailto"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	subscription_store "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/principal"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

type uploadAddDeps struct {
	route         server.Route
	store         *upload_store.Store
	consumerStore *consumer_store.Store
}

func newUploadAddDeps(t *testing.T, uploadService principal.Signer, logger *zap.Logger) *uploadAddDeps {
	t.Helper()
	consumerStore := consumer_store.New()
	provisioningSvc := provisioning.NewService(
		[]did.DID{uploadService.DID()},
		consumerStore,
		subscription_store.New(),
	)
	store := upload_store.New()
	route := handlers.NewUploadAddHandler(provisioningSvc, store, logger)
	return &uploadAddDeps{route: route, store: store, consumerStore: consumerStore}
}

// invokeUploadAdd builds an /upload/add invocation with the given args and a
// signed response ready for the handler.
func invokeUploadAdd(
	t *testing.T,
	ctx context.Context,
	agent principal.Signer,
	uploadService principal.Signer,
	space principal.Signer,
	args *uploadcmds.AddArguments,
) (execution.Request, *execution.ExecResponse) {
	t.Helper()
	inv, err := uploadcmds.Add.Invoke(
		agent,
		space.DID(),
		args,
		invocation.WithAudience(uploadService.DID()),
	)
	require.NoError(t, err)
	req := execution.NewRequest(ctx, inv)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
	require.NoError(t, err)
	return req, res
}

// provisionUploadSpace adds a consumer record so the upload service shows up as
// a provider for the space when the handler calls ListServiceProviders.
func provisionUploadSpace(t *testing.T, consumerStore *consumer_store.Store, uploadService principal.Signer, space principal.Signer) {
	t.Helper()
	account := testutil.Must(didmailto.New("alice@example.com"))(t)
	require.NoError(t, consumerStore.Add(
		t.Context(),
		uploadService.DID(),
		space.DID(),
		account,
		"sub-1",
		testutil.RandomCID(t),
	))
}

func TestUploadAddHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService
	alice := testutil.Alice

	t.Run("space not provisioned", func(t *testing.T) {
		deps := newUploadAddDeps(t, uploadService, logger)

		space := testutil.RandomSigner(t)
		root := testutil.RandomCID(t)
		req, res := invokeUploadAdd(t, ctx, alice, uploadService, space, &uploadcmds.AddArguments{Root: root})

		err := deps.route.Handler(req, res)
		require.NoError(t, err)

		_, err = uploadcmds.Add.Unpack(res.Receipt())
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		require.Equal(t, accesscmds.InsufficientStorageErrorName, errModel.Name())

		// Nothing should have been persisted.
		exists, err := deps.store.Exists(ctx, space.DID(), root)
		require.NoError(t, err)
		require.False(t, exists)
	})

	t.Run("success with no shards", func(t *testing.T) {
		deps := newUploadAddDeps(t, uploadService, logger)

		space := testutil.RandomSigner(t)
		provisionUploadSpace(t, deps.consumerStore, uploadService, space)

		root := testutil.RandomCID(t)
		req, res := invokeUploadAdd(t, ctx, alice, uploadService, space, &uploadcmds.AddArguments{Root: root})

		err := deps.route.Handler(req, res)
		require.NoError(t, err)

				_, err = blobcmds.Allocate.Unpack(res.Receipt())
		require.NoError(t, err)

		// Upload should be persisted.
		exists, err := deps.store.Exists(ctx, space.DID(), root)
		require.NoError(t, err)
		require.True(t, exists)

		// Unrelated CID should not be present.
		exists, err = deps.store.Exists(ctx, space.DID(), testutil.RandomCID(t))
		require.NoError(t, err)
		require.False(t, exists)
	})

	t.Run("success with shards", func(t *testing.T) {
		deps := newUploadAddDeps(t, uploadService, logger)

		space := testutil.RandomSigner(t)
		provisionUploadSpace(t, deps.consumerStore, uploadService, space)

		root := testutil.RandomCID(t)
		shard1 := testutil.RandomCID(t)
		shard2 := testutil.RandomCID(t)

		req, res := invokeUploadAdd(t, ctx, alice, uploadService, space, &uploadcmds.AddArguments{
			Root:   root,
			Shards: []cid.Cid{shard1, shard2},
		})

		err := deps.route.Handler(req, res)
		require.NoError(t, err)

				_, err = blobcmds.Allocate.Unpack(res.Receipt())
		require.NoError(t, err)

		exists, err := deps.store.Exists(ctx, space.DID(), root)
		require.NoError(t, err)
		require.True(t, exists)
	})

	t.Run("success with index", func(t *testing.T) {
		deps := newUploadAddDeps(t, uploadService, logger)

		space := testutil.RandomSigner(t)
		provisionUploadSpace(t, deps.consumerStore, uploadService, space)

		root := testutil.RandomCID(t)
		index := testutil.RandomCID(t)

		req, res := invokeUploadAdd(t, ctx, alice, uploadService, space, &uploadcmds.AddArguments{
			Root:  root,
			Index: &index,
		})

		err := deps.route.Handler(req, res)
		require.NoError(t, err)

				_, err = blobcmds.Allocate.Unpack(res.Receipt())
		require.NoError(t, err)

		exists, err := deps.store.Exists(ctx, space.DID(), root)
		require.NoError(t, err)
		require.True(t, exists)
	})

	t.Run("upsert updates existing upload", func(t *testing.T) {
		deps := newUploadAddDeps(t, uploadService, logger)

		space := testutil.RandomSigner(t)
		provisionUploadSpace(t, deps.consumerStore, uploadService, space)

		root := testutil.RandomCID(t)
		shard1 := testutil.RandomCID(t)

		req1, res1 := invokeUploadAdd(t, ctx, alice, uploadService, space, &uploadcmds.AddArguments{
			Root:   root,
			Shards: []cid.Cid{shard1},
		})
		require.NoError(t, deps.route.Handler(req1, res1))
		require.False(t, res1.Receipt().Out().IsErr())

		// Add again with a new shard.
		shard2 := testutil.RandomCID(t)
		req2, res2 := invokeUploadAdd(t, ctx, alice, uploadService, space, &uploadcmds.AddArguments{
			Root:   root,
			Shards: []cid.Cid{shard2},
		})
		require.NoError(t, deps.route.Handler(req2, res2))
		require.False(t, res2.Receipt().Out().IsErr())

		// Upload should still exist.
		exists, err := deps.store.Exists(ctx, space.DID(), root)
		require.NoError(t, err)
		require.True(t, exists)
	})
}
