package handlers_test

import (
	"bytes"
	"context"
	"testing"

	cmdshard "github.com/fil-forge/libforge/commands/upload/shard"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload/memory"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/principal"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// invokeUploadShardList builds an /upload/shard/list invocation with the given
// args and a signed response ready for the handler.
func invokeUploadShardList(
	t *testing.T,
	ctx context.Context,
	agent principal.Signer,
	uploadService principal.Signer,
	space principal.Signer,
	args *cmdshard.ListArguments,
) (execution.Request, *execution.ExecResponse) {
	t.Helper()
	inv, err := cmdshard.List.Invoke(
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

func TestUploadShardListHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService
	alice := testutil.Alice

	t.Run("empty shards", func(t *testing.T) {
		store := upload_store.New()
		handler := handlers.NewUploadShardListHandler(store, logger)

		space := testutil.RandomSigner(t)
		root := testutil.RandomCID(t)

		// Upload exists with no shards.
		require.NoError(t, store.Upsert(ctx, space.DID(), root, nil, nil, testutil.RandomCID(t)))

		req, res := invokeUploadShardList(t, ctx, alice, uploadService, space, &cmdshard.ListArguments{Root: root})

		err := handler.Handler(req, res)
		require.NoError(t, err)

		o, x := res.Receipt().Out().Unpack()
		require.Nil(t, x)
		require.NotNil(t, o)

		var ok cmdshard.ListOK
		require.NoError(t, ok.UnmarshalCBOR(bytes.NewReader(o)))
		require.Empty(t, ok.Results)
	})

	t.Run("lists shards", func(t *testing.T) {
		store := upload_store.New()
		handler := handlers.NewUploadShardListHandler(store, logger)

		space := testutil.RandomSigner(t)
		root := testutil.RandomCID(t)
		shard1 := testutil.RandomCID(t)
		shard2 := testutil.RandomCID(t)

		require.NoError(t, store.Upsert(ctx, space.DID(), root, nil, []cid.Cid{shard1, shard2}, testutil.RandomCID(t)))

		req, res := invokeUploadShardList(t, ctx, alice, uploadService, space, &cmdshard.ListArguments{Root: root})

		err := handler.Handler(req, res)
		require.NoError(t, err)

		o, x := res.Receipt().Out().Unpack()
		require.Nil(t, x)
		var ok cmdshard.ListOK
		require.NoError(t, ok.UnmarshalCBOR(bytes.NewReader(o)))
		require.Len(t, ok.Results, 2)

		got := map[string]bool{}
		for _, c := range ok.Results {
			got[c.String()] = true
		}
		require.True(t, got[shard1.String()])
		require.True(t, got[shard2.String()])
	})

	t.Run("with size limit", func(t *testing.T) {
		store := upload_store.New()
		handler := handlers.NewUploadShardListHandler(store, logger)

		space := testutil.RandomSigner(t)
		root := testutil.RandomCID(t)
		shard1 := testutil.RandomCID(t)
		shard2 := testutil.RandomCID(t)
		shard3 := testutil.RandomCID(t)
		require.NoError(t, store.Upsert(ctx, space.DID(), root, nil, []cid.Cid{shard1, shard2, shard3}, testutil.RandomCID(t)))

		size := uint64(2)
		req, res := invokeUploadShardList(t, ctx, alice, uploadService, space, &cmdshard.ListArguments{Root: root, Size: &size})

		err := handler.Handler(req, res)
		require.NoError(t, err)

		o, x := res.Receipt().Out().Unpack()
		require.Nil(t, x)
		var ok cmdshard.ListOK
		require.NoError(t, ok.UnmarshalCBOR(bytes.NewReader(o)))
		require.Len(t, ok.Results, 2)
		require.NotNil(t, ok.Cursor)
	})

	t.Run("with cursor pagination", func(t *testing.T) {
		store := upload_store.New()
		handler := handlers.NewUploadShardListHandler(store, logger)

		space := testutil.RandomSigner(t)
		root := testutil.RandomCID(t)
		shard1 := testutil.RandomCID(t)
		shard2 := testutil.RandomCID(t)
		shard3 := testutil.RandomCID(t)
		require.NoError(t, store.Upsert(ctx, space.DID(), root, nil, []cid.Cid{shard1, shard2, shard3}, testutil.RandomCID(t)))

		size := uint64(1)
		req1, res1 := invokeUploadShardList(t, ctx, alice, uploadService, space, &cmdshard.ListArguments{Root: root, Size: &size})
		require.NoError(t, handler.Handler(req1, res1))

		o1, x := res1.Receipt().Out().Unpack()
		require.Nil(t, x)
		var ok1 cmdshard.ListOK
		require.NoError(t, ok1.UnmarshalCBOR(bytes.NewReader(o1)))
		require.Len(t, ok1.Results, 1)
		require.NotNil(t, ok1.Cursor)

		// Second page using cursor.
		cursor := *ok1.Cursor
		req2, res2 := invokeUploadShardList(t, ctx, alice, uploadService, space, &cmdshard.ListArguments{Root: root, Cursor: &cursor, Size: &size})
		require.NoError(t, handler.Handler(req2, res2))

		o2, x := res2.Receipt().Out().Unpack()
		require.Nil(t, x)
		var ok2 cmdshard.ListOK
		require.NoError(t, ok2.UnmarshalCBOR(bytes.NewReader(o2)))
		require.Len(t, ok2.Results, 1)
		require.NotEqual(t, ok1.Results[0].String(), ok2.Results[0].String())
	})
}
