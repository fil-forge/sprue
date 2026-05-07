package handlers_test

import (
	"testing"

	"github.com/fil-forge/go-libstoracha/capabilities/upload"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure/datamodel"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	uploadmemory "github.com/fil-forge/sprue/pkg/store/upload/memory"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestUploadListHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService
	alice := testutil.Alice

	t.Run("invalid space DID", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadListHandler(store, logger)

		caveats := upload.ListCaveats{}
		inv, err := upload.List.Invoke(alice, uploadService, "not-a-did", caveats)
		require.NoError(t, err)

		cap := upload.List.New("not-a-did", caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)

		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
		require.NotNil(t, model.Name)
		require.Equal(t, handlers.InvalidSpaceErrorName, *model.Name)
	})

	t.Run("empty list", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadListHandler(store, logger)

		space := testutil.RandomSigner(t)
		caveats := upload.ListCaveats{}
		inv, err := upload.List.Invoke(alice, uploadService, space.DID().String(), caveats)
		require.NoError(t, err)

		cap := upload.List.New(space.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Empty(t, ok.Results)
		require.Nil(t, ok.Cursor)
	})

	t.Run("lists uploads", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadListHandler(store, logger)

		space := testutil.RandomSigner(t)
		root1 := testutil.RandomCID(t)
		root2 := testutil.RandomCID(t)

		err := store.Upsert(ctx, space.DID(), root1, nil, testutil.RandomCID(t))
		require.NoError(t, err)
		err = store.Upsert(ctx, space.DID(), root2, nil, testutil.RandomCID(t))
		require.NoError(t, err)

		caveats := upload.ListCaveats{}
		inv, err := upload.List.Invoke(alice, uploadService, space.DID().String(), caveats)
		require.NoError(t, err)

		cap := upload.List.New(space.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Len(t, ok.Results, 2)

		roots := make(map[string]bool)
		for _, item := range ok.Results {
			roots[item.Root.String()] = true
		}
		require.True(t, roots[cidlink.Link{Cid: root1}.String()])
		require.True(t, roots[cidlink.Link{Cid: root2}.String()])
	})

	t.Run("with size limit", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadListHandler(store, logger)

		space := testutil.RandomSigner(t)

		for i := 0; i < 3; i++ {
			err := store.Upsert(ctx, space.DID(), testutil.RandomCID(t), nil, testutil.RandomCID(t))
			require.NoError(t, err)
		}

		size := uint64(2)
		caveats := upload.ListCaveats{Size: &size}
		inv, err := upload.List.Invoke(alice, uploadService, space.DID().String(), caveats)
		require.NoError(t, err)

		cap := upload.List.New(space.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Len(t, ok.Results, 2)
		require.NotNil(t, ok.Cursor)
	})

	t.Run("with cursor pagination", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadListHandler(store, logger)

		space := testutil.RandomSigner(t)

		err := store.Upsert(ctx, space.DID(), testutil.RandomCID(t), nil, testutil.RandomCID(t))
		require.NoError(t, err)
		err = store.Upsert(ctx, space.DID(), testutil.RandomCID(t), nil, testutil.RandomCID(t))
		require.NoError(t, err)
		err = store.Upsert(ctx, space.DID(), testutil.RandomCID(t), nil, testutil.RandomCID(t))
		require.NoError(t, err)

		// First page: size 1
		size := uint64(1)
		caveats1 := upload.ListCaveats{Size: &size}
		inv1, err := upload.List.Invoke(alice, uploadService, space.DID().String(), caveats1)
		require.NoError(t, err)

		cap1 := upload.List.New(space.DID().String(), caveats1)

		res1, _, err := handler(ctx, cap1, inv1, nil)
		require.NoError(t, err)

		ok1, fail := result.Unwrap(res1)
		require.Nil(t, fail)
		require.Len(t, ok1.Results, 1)
		require.NotNil(t, ok1.Cursor)

		// Second page using cursor
		cursor := *ok1.Cursor
		caveats2 := upload.ListCaveats{Cursor: &cursor, Size: &size}
		inv2, err := upload.List.Invoke(alice, uploadService, space.DID().String(), caveats2)
		require.NoError(t, err)

		cap2 := upload.List.New(space.DID().String(), caveats2)

		res2, _, err := handler(ctx, cap2, inv2, nil)
		require.NoError(t, err)

		ok2, fail := result.Unwrap(res2)
		require.Nil(t, fail)
		require.Len(t, ok2.Results, 1)

		// Results should be different
		require.NotEqual(t, ok1.Results[0].Root.String(), ok2.Results[0].Root.String())
	})

	t.Run("does not list uploads from other spaces", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadListHandler(store, logger)

		space1 := testutil.RandomSigner(t)
		space2 := testutil.RandomSigner(t)

		err := store.Upsert(ctx, space1.DID(), testutil.RandomCID(t), nil, testutil.RandomCID(t))
		require.NoError(t, err)

		caveats := upload.ListCaveats{}
		inv, err := upload.List.Invoke(alice, uploadService, space2.DID().String(), caveats)
		require.NoError(t, err)

		cap := upload.List.New(space2.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Empty(t, ok.Results)
	})
}
