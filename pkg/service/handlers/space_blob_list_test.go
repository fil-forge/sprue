package handlers_test

import (
	"testing"

	"github.com/fil-forge/go-libstoracha/capabilities/space/blob"
	"github.com/fil-forge/go-libstoracha/capabilities/types"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure/datamodel"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/lib/didmailto"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestSpaceBlobListHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService
	alice := testutil.Alice
	aliceAccount := testutil.Must(didmailto.Parse("did:mailto:example.com:alice"))(t)

	t.Run("invalid space DID", func(t *testing.T) {
		blobRegistry, _ := newBlobRegistry()
		handler := handlers.SpaceBlobListHandler(blobRegistry, logger)

		caveats := blob.ListCaveats{}
		inv, err := blob.List.Invoke(alice, uploadService, "not-a-did", caveats)
		require.NoError(t, err)

		cap := blob.List.New("not-a-did", caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)

		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
		require.NotNil(t, model.Name)
		require.Equal(t, handlers.InvalidSpaceErrorName, *model.Name)
	})

	t.Run("empty list", func(t *testing.T) {
		blobRegistry, _ := newBlobRegistry()
		handler := handlers.SpaceBlobListHandler(blobRegistry, logger)

		space := testutil.RandomSigner(t)
		caveats := blob.ListCaveats{}
		inv, err := blob.List.Invoke(alice, uploadService, space.DID().String(), caveats)
		require.NoError(t, err)

		cap := blob.List.New(space.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Empty(t, ok.Results)
	})

	t.Run("lists blobs", func(t *testing.T) {
		blobRegistry, consumerStore := newBlobRegistry()
		handler := handlers.SpaceBlobListHandler(blobRegistry, logger)

		space := testutil.RandomSigner(t)

		// Provision the space
		err := consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "", testutil.RandomCID(t))
		require.NoError(t, err)

		blob1 := types.Blob{Digest: testutil.RandomMultihash(t), Size: 100}
		blob2 := types.Blob{Digest: testutil.RandomMultihash(t), Size: 200}

		err = blobRegistry.Register(ctx, space.DID(), blob1, testutil.RandomCID(t))
		require.NoError(t, err)
		err = blobRegistry.Register(ctx, space.DID(), blob2, testutil.RandomCID(t))
		require.NoError(t, err)

		caveats := blob.ListCaveats{}
		inv, err := blob.List.Invoke(alice, uploadService, space.DID().String(), caveats)
		require.NoError(t, err)

		cap := blob.List.New(space.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Len(t, ok.Results, 2)
	})

	t.Run("with size limit", func(t *testing.T) {
		blobRegistry, consumerStore := newBlobRegistry()
		handler := handlers.SpaceBlobListHandler(blobRegistry, logger)

		space := testutil.RandomSigner(t)

		err := consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "", testutil.RandomCID(t))
		require.NoError(t, err)

		for i := range 3 {
			err := blobRegistry.Register(ctx, space.DID(), types.Blob{Digest: testutil.RandomMultihash(t), Size: uint64(i + 1)}, testutil.RandomCID(t))
			require.NoError(t, err)
		}

		size := uint64(2)
		caveats := blob.ListCaveats{Size: &size}
		inv, err := blob.List.Invoke(alice, uploadService, space.DID().String(), caveats)
		require.NoError(t, err)

		cap := blob.List.New(space.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Len(t, ok.Results, 2)
		require.NotNil(t, ok.Cursor)
	})

	t.Run("with cursor pagination", func(t *testing.T) {
		blobRegistry, consumerStore := newBlobRegistry()
		handler := handlers.SpaceBlobListHandler(blobRegistry, logger)

		space := testutil.RandomSigner(t)

		err := consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "", testutil.RandomCID(t))
		require.NoError(t, err)

		for i := range 3 {
			err := blobRegistry.Register(ctx, space.DID(), types.Blob{Digest: testutil.RandomMultihash(t), Size: uint64(i + 1)}, testutil.RandomCID(t))
			require.NoError(t, err)
		}

		// First page: size 1
		size := uint64(1)
		caveats1 := blob.ListCaveats{Size: &size}
		inv1, err := blob.List.Invoke(alice, uploadService, space.DID().String(), caveats1)
		require.NoError(t, err)

		cap1 := blob.List.New(space.DID().String(), caveats1)

		res1, _, err := handler(ctx, cap1, inv1, nil)
		require.NoError(t, err)

		ok1, fail := result.Unwrap(res1)
		require.Nil(t, fail)
		require.Len(t, ok1.Results, 1)
		require.NotNil(t, ok1.Cursor)

		// Second page using cursor
		cursor := *ok1.Cursor
		caveats2 := blob.ListCaveats{Cursor: &cursor, Size: &size}
		inv2, err := blob.List.Invoke(alice, uploadService, space.DID().String(), caveats2)
		require.NoError(t, err)

		cap2 := blob.List.New(space.DID().String(), caveats2)

		res2, _, err := handler(ctx, cap2, inv2, nil)
		require.NoError(t, err)

		ok2, fail := result.Unwrap(res2)
		require.Nil(t, fail)
		require.Len(t, ok2.Results, 1)

		// Results should be different blobs
		require.NotEqual(t, ok1.Results[0].Blob.Digest.HexString(), ok2.Results[0].Blob.Digest.HexString())
	})

	t.Run("does not list blobs from other spaces", func(t *testing.T) {
		blobRegistry, consumerStore := newBlobRegistry()
		handler := handlers.SpaceBlobListHandler(blobRegistry, logger)

		space1 := testutil.RandomSigner(t)
		space2 := testutil.RandomSigner(t)

		err := consumerStore.Add(ctx, uploadService.DID(), space1.DID(), aliceAccount, "", testutil.RandomCID(t))
		require.NoError(t, err)

		err = blobRegistry.Register(ctx, space1.DID(), types.Blob{Digest: testutil.RandomMultihash(t), Size: 100}, testutil.RandomCID(t))
		require.NoError(t, err)

		caveats := blob.ListCaveats{}
		inv, err := blob.List.Invoke(alice, uploadService, space2.DID().String(), caveats)
		require.NoError(t, err)

		cap := blob.List.New(space2.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Empty(t, ok.Results)
	})
}
