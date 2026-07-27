package handlers_test

import (
	"context"
	"testing"

	uploadcmds "github.com/fil-forge/libforge/commands/upload"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload/memory"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func invokeUploadRemove(
	t *testing.T,
	ctx context.Context,
	route server.Route,
	agent ucan.Issuer,
	uploadService ucan.Issuer,
	space ucan.Principal,
	root cid.Cid,
) ucan.Receipt {
	t.Helper()
	inv, err := uploadcmds.Remove.Invoke(
		agent,
		space.DID(),
		&uploadcmds.RemoveArguments{Root: root},
		invocation.WithAudience(uploadService.DID()),
	)
	require.NoError(t, err)
	req := execution.NewRequest(ctx, inv)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
	require.NoError(t, err)
	require.NoError(t, route.Handler(req, res))
	return res.Receipt()
}

func TestUploadRemoveHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService
	alice := testutil.Alice

	t.Run("removes existing upload", func(t *testing.T) {
		store := upload_store.New()
		route := handlers.NewUploadRemoveHandler(store, logger)

		space := testutil.RandomIssuer(t)
		root := testutil.RandomCID(t)
		require.NoError(t, store.Upsert(ctx, space.DID(), root, nil, nil, testutil.RandomCID(t)))

		rcpt := invokeUploadRemove(t, ctx, route, alice, uploadService, space, root)
		_, err := uploadcmds.Remove.Unpack(rcpt)
		require.NoError(t, err)

		exists, err := store.Exists(ctx, space.DID(), root)
		require.NoError(t, err)
		require.False(t, exists, "upload removed")
	})

	t.Run("unknown root is idempotent success", func(t *testing.T) {
		store := upload_store.New()
		route := handlers.NewUploadRemoveHandler(store, logger)

		space := testutil.RandomIssuer(t)
		rcpt := invokeUploadRemove(t, ctx, route, alice, uploadService, space, testutil.RandomCID(t))
		_, err := uploadcmds.Remove.Unpack(rcpt)
		require.NoError(t, err)
	})

	t.Run("only removes the requested space's entry", func(t *testing.T) {
		store := upload_store.New()
		route := handlers.NewUploadRemoveHandler(store, logger)

		spaceA := testutil.RandomIssuer(t)
		spaceB := testutil.RandomIssuer(t)
		root := testutil.RandomCID(t)
		require.NoError(t, store.Upsert(ctx, spaceA.DID(), root, nil, nil, testutil.RandomCID(t)))
		require.NoError(t, store.Upsert(ctx, spaceB.DID(), root, nil, nil, testutil.RandomCID(t)))

		rcpt := invokeUploadRemove(t, ctx, route, alice, uploadService, spaceA, root)
		_, err := uploadcmds.Remove.Unpack(rcpt)
		require.NoError(t, err)

		exists, err := store.Exists(ctx, spaceA.DID(), root)
		require.NoError(t, err)
		require.False(t, exists)
		exists, err = store.Exists(ctx, spaceB.DID(), root)
		require.NoError(t, err)
		require.True(t, exists, "other space's upload retained")
	})
}
