package handlers_test

import (
	"testing"

	"github.com/fil-forge/go-libstoracha/capabilities/upload/shard"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure/datamodel"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	uploadmemory "github.com/fil-forge/sprue/pkg/store/upload/memory"
	"github.com/ipfs/go-cid"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestUploadShardListHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService
	alice := testutil.Alice

	t.Run("invalid space DID", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadShardListHandler(store, logger)

		root := cidlink.Link{Cid: testutil.RandomCID(t)}
		caveats := shard.ListCaveats{Root: root}
		inv, err := shard.List.Invoke(alice, uploadService, "not-a-did", caveats)
		require.NoError(t, err)

		cap := shard.List.New("not-a-did", caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)

		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
		require.NotNil(t, model.Name)
		require.Equal(t, handlers.InvalidSpaceErrorName, *model.Name)
	})

	t.Run("empty shards", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadShardListHandler(store, logger)

		space := testutil.RandomSigner(t)
		root := testutil.RandomCID(t)

		// Create upload with no shards
		err := store.Upsert(ctx, space.DID(), root, nil, testutil.RandomCID(t))
		require.NoError(t, err)

		rootLink := cidlink.Link{Cid: root}
		caveats := shard.ListCaveats{Root: rootLink}
		inv, err := shard.List.Invoke(alice, uploadService, space.DID().String(), caveats)
		require.NoError(t, err)

		cap := shard.List.New(space.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Empty(t, ok.Results)
	})

	t.Run("lists shards", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadShardListHandler(store, logger)

		space := testutil.RandomSigner(t)
		root := testutil.RandomCID(t)
		shard1 := testutil.RandomCID(t)
		shard2 := testutil.RandomCID(t)

		err := store.Upsert(ctx, space.DID(), root, []cid.Cid{shard1, shard2}, testutil.RandomCID(t))
		require.NoError(t, err)

		rootLink := cidlink.Link{Cid: root}
		caveats := shard.ListCaveats{Root: rootLink}
		inv, err := shard.List.Invoke(alice, uploadService, space.DID().String(), caveats)
		require.NoError(t, err)

		cap := shard.List.New(space.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Len(t, ok.Results, 2)
	})

	t.Run("with cursor pagination", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadShardListHandler(store, logger)

		space := testutil.RandomSigner(t)
		root := testutil.RandomCID(t)
		shard1 := testutil.RandomCID(t)
		shard2 := testutil.RandomCID(t)
		shard3 := testutil.RandomCID(t)

		err := store.Upsert(ctx, space.DID(), root, []cid.Cid{shard1, shard2, shard3}, testutil.RandomCID(t))
		require.NoError(t, err)

		rootLink := cidlink.Link{Cid: root}

		// First page: size 1
		size := uint64(1)
		caveats1 := shard.ListCaveats{Root: rootLink, Size: &size}
		inv1, err := shard.List.Invoke(alice, uploadService, space.DID().String(), caveats1)
		require.NoError(t, err)

		cap1 := shard.List.New(space.DID().String(), caveats1)

		res1, _, err := handler(ctx, cap1, inv1, nil)
		require.NoError(t, err)

		ok1, fail := result.Unwrap(res1)
		require.Nil(t, fail)
		require.Len(t, ok1.Results, 1)
		require.NotNil(t, ok1.Cursor)

		// Second page using cursor
		cursor := *ok1.Cursor
		caveats2 := shard.ListCaveats{Root: rootLink, Cursor: &cursor, Size: &size}
		inv2, err := shard.List.Invoke(alice, uploadService, space.DID().String(), caveats2)
		require.NoError(t, err)

		cap2 := shard.List.New(space.DID().String(), caveats2)

		res2, _, err := handler(ctx, cap2, inv2, nil)
		require.NoError(t, err)

		ok2, fail := result.Unwrap(res2)
		require.Nil(t, fail)
		require.Len(t, ok2.Results, 1)

		// Results should be different
		require.NotEqual(t, ok1.Results[0].String(), ok2.Results[0].String())
	})
}
