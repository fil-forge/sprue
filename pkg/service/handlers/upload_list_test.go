package handlers_test

import (
	"context"
	"testing"

	uploadcaps "github.com/fil-forge/libforge/capabilities/upload"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload/memory"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/principal"
	"github.com/fil-forge/ucantone/result"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// invokeUploadList builds an /upload/list invocation with the given args and a
// signed response ready for the handler.
func invokeUploadList(
	t *testing.T,
	ctx context.Context,
	agent principal.Signer,
	uploadService principal.Signer,
	space principal.Signer,
	args *uploadcaps.ListArguments,
) (execution.Request, *execution.ExecResponse) {
	t.Helper()
	inv, err := uploadcaps.List.Invoke(
		agent,
		space,
		args,
		invocation.WithAudience(uploadService),
	)
	require.NoError(t, err)
	req := execution.NewRequest(ctx, inv)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
	require.NoError(t, err)
	return req, res
}

func TestUploadListHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService
	alice := testutil.Alice

	t.Run("empty list", func(t *testing.T) {
		store := upload_store.New()
		handler := handlers.NewUploadListHandler(store, logger)

		space := testutil.RandomSigner(t)
		req, res := invokeUploadList(t, ctx, alice, uploadService, space, &uploadcaps.ListArguments{})

		err := handler.Handler(req, res)
		require.NoError(t, err)

		o, fail := result.Unwrap(res.Receipt().Out())
		require.Nil(t, fail)
		require.NotNil(t, o)

		ok := uploadcaps.ListOK{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(o), &ok))
		require.Empty(t, ok.Results)
		require.Nil(t, ok.Cursor)
	})

	t.Run("lists uploads", func(t *testing.T) {
		store := upload_store.New()
		handler := handlers.NewUploadListHandler(store, logger)

		space := testutil.RandomSigner(t)
		root1 := testutil.RandomCID(t)
		root2 := testutil.RandomCID(t)

		require.NoError(t, store.Upsert(ctx, space.DID(), root1, nil, nil, testutil.RandomCID(t)))
		require.NoError(t, store.Upsert(ctx, space.DID(), root2, nil, nil, testutil.RandomCID(t)))

		req, res := invokeUploadList(t, ctx, alice, uploadService, space, &uploadcaps.ListArguments{})

		err := handler.Handler(req, res)
		require.NoError(t, err)

		o, fail := result.Unwrap(res.Receipt().Out())
		require.Nil(t, fail)
		ok := uploadcaps.ListOK{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(o), &ok))
		require.Len(t, ok.Results, 2)

		roots := map[string]bool{}
		for _, item := range ok.Results {
			roots[item.Root.String()] = true
		}
		require.True(t, roots[root1.String()])
		require.True(t, roots[root2.String()])
	})

	t.Run("with size limit", func(t *testing.T) {
		store := upload_store.New()
		handler := handlers.NewUploadListHandler(store, logger)

		space := testutil.RandomSigner(t)
		for range 3 {
			require.NoError(t, store.Upsert(ctx, space.DID(), testutil.RandomCID(t), nil, nil, testutil.RandomCID(t)))
		}

		size := int64(2)
		req, res := invokeUploadList(t, ctx, alice, uploadService, space, &uploadcaps.ListArguments{Size: &size})

		err := handler.Handler(req, res)
		require.NoError(t, err)

		o, fail := result.Unwrap(res.Receipt().Out())
		require.Nil(t, fail)
		ok := uploadcaps.ListOK{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(o), &ok))
		require.Len(t, ok.Results, 2)
		require.NotNil(t, ok.Cursor)
	})

	t.Run("with cursor pagination", func(t *testing.T) {
		store := upload_store.New()
		handler := handlers.NewUploadListHandler(store, logger)

		space := testutil.RandomSigner(t)
		for range 3 {
			require.NoError(t, store.Upsert(ctx, space.DID(), testutil.RandomCID(t), nil, nil, testutil.RandomCID(t)))
		}

		size := int64(1)
		req1, res1 := invokeUploadList(t, ctx, alice, uploadService, space, &uploadcaps.ListArguments{Size: &size})
		require.NoError(t, handler.Handler(req1, res1))

		o1, fail := result.Unwrap(res1.Receipt().Out())
		require.Nil(t, fail)
		ok1 := uploadcaps.ListOK{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(o1), &ok1))
		require.Len(t, ok1.Results, 1)
		require.NotNil(t, ok1.Cursor)

		// Second page using cursor.
		cursor := *ok1.Cursor
		req2, res2 := invokeUploadList(t, ctx, alice, uploadService, space, &uploadcaps.ListArguments{Cursor: &cursor, Size: &size})
		require.NoError(t, handler.Handler(req2, res2))

		o2, fail := result.Unwrap(res2.Receipt().Out())
		require.Nil(t, fail)
		ok2 := uploadcaps.ListOK{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(o2), &ok2))
		require.Len(t, ok2.Results, 1)
		require.NotEqual(t, ok1.Results[0].Root.String(), ok2.Results[0].Root.String())
	})

	t.Run("does not list uploads from other spaces", func(t *testing.T) {
		store := upload_store.New()
		handler := handlers.NewUploadListHandler(store, logger)

		space1 := testutil.RandomSigner(t)
		space2 := testutil.RandomSigner(t)

		require.NoError(t, store.Upsert(ctx, space1.DID(), testutil.RandomCID(t), nil, nil, testutil.RandomCID(t)))

		// Query space2 — should be empty.
		req, res := invokeUploadList(t, ctx, alice, uploadService, space2, &uploadcaps.ListArguments{})
		require.NoError(t, handler.Handler(req, res))

		o, fail := result.Unwrap(res.Receipt().Out())
		require.Nil(t, fail)
		ok := uploadcaps.ListOK{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(o), &ok))
		require.Empty(t, ok.Results)
	})

	t.Run("preserves optional index pointer", func(t *testing.T) {
		store := upload_store.New()
		handler := handlers.NewUploadListHandler(store, logger)

		space := testutil.RandomSigner(t)
		root := testutil.RandomCID(t)
		index := testutil.RandomCID(t)
		require.NoError(t, store.Upsert(ctx, space.DID(), root, &index, nil, testutil.RandomCID(t)))

		req, res := invokeUploadList(t, ctx, alice, uploadService, space, &uploadcaps.ListArguments{})

		require.NoError(t, handler.Handler(req, res))

		o, fail := result.Unwrap(res.Receipt().Out())
		require.Nil(t, fail)
		ok := uploadcaps.ListOK{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(o), &ok))
		require.Len(t, ok.Results, 1)
		require.NotNil(t, ok.Results[0].Index)
		require.Equal(t, cid.Cid(index), *ok.Results[0].Index)
	})
}
