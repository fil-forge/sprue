package handlers_test

import (
	"testing"

	"github.com/fil-forge/go-libstoracha/capabilities/upload"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure/datamodel"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	uploadmemory "github.com/fil-forge/sprue/pkg/store/upload/memory"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestUploadAddHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService
	alice := testutil.Alice

	t.Run("invalid space DID", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadAddHandler(store, logger)

		root := cidlink.Link{Cid: testutil.RandomCID(t)}
		cap := ucan.NewCapability(
			upload.AddAbility,
			"not-a-did",
			upload.AddCaveats{Root: root},
		)

		inv, err := invocation.Invoke(alice, uploadService, cap)
		require.NoError(t, err)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)

		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
		require.NotNil(t, model.Name)
		require.Equal(t, handlers.InvalidSpaceErrorName, *model.Name)
	})

	t.Run("success with no shards", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadAddHandler(store, logger)

		space := testutil.RandomSigner(t)
		root := cidlink.Link{Cid: testutil.RandomCID(t)}
		cap := ucan.NewCapability(
			upload.AddAbility,
			space.DID().String(),
			upload.AddCaveats{Root: root},
		)

		inv, err := invocation.Invoke(alice, uploadService, cap)
		require.NoError(t, err)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Equal(t, root.String(), ok.Root.String())

		// Verify persisted
		exists, err := store.Exists(ctx, space.DID(), testutil.RandomCID(t))
		require.NoError(t, err)
		require.False(t, exists)

		exists, err = store.Exists(ctx, space.DID(), root.Cid)
		require.NoError(t, err)
		require.True(t, exists)
	})

	t.Run("success with shards", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadAddHandler(store, logger)

		space := testutil.RandomSigner(t)
		root := cidlink.Link{Cid: testutil.RandomCID(t)}
		shard1 := cidlink.Link{Cid: testutil.RandomCID(t)}
		shard2 := cidlink.Link{Cid: testutil.RandomCID(t)}

		cap := ucan.NewCapability(
			upload.AddAbility,
			space.DID().String(),
			upload.AddCaveats{
				Root:   root,
				Shards: []ucan.Link{shard1, shard2},
			},
		)

		inv, err := invocation.Invoke(alice, uploadService, cap)
		require.NoError(t, err)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Equal(t, root.String(), ok.Root.String())

		// Verify upload exists
		exists, err := store.Exists(ctx, space.DID(), root.Cid)
		require.NoError(t, err)
		require.True(t, exists)
	})

	t.Run("upsert updates existing upload", func(t *testing.T) {
		store := uploadmemory.New()
		handler := handlers.UploadAddHandler(store, logger)

		space := testutil.RandomSigner(t)
		root := cidlink.Link{Cid: testutil.RandomCID(t)}
		shard1 := cidlink.Link{Cid: testutil.RandomCID(t)}

		cap1 := ucan.NewCapability(
			upload.AddAbility,
			space.DID().String(),
			upload.AddCaveats{
				Root:   root,
				Shards: []ucan.Link{shard1},
			},
		)

		inv1, err := invocation.Invoke(alice, uploadService, cap1)
		require.NoError(t, err)

		res1, _, err := handler(ctx, cap1, inv1, nil)
		require.NoError(t, err)
		_, fail1 := result.Unwrap(res1)
		require.Nil(t, fail1)

		// Add again with a new shard
		shard2 := cidlink.Link{Cid: testutil.RandomCID(t)}
		cap2 := ucan.NewCapability(
			upload.AddAbility,
			space.DID().String(),
			upload.AddCaveats{
				Root:   root,
				Shards: []ucan.Link{shard2},
			},
		)

		inv2, err := invocation.Invoke(alice, uploadService, cap2)
		require.NoError(t, err)

		res2, _, err := handler(ctx, cap2, inv2, nil)
		require.NoError(t, err)
		_, fail2 := result.Unwrap(res2)
		require.Nil(t, fail2)

		// Upload should still exist
		exists, err := store.Exists(ctx, space.DID(), root.Cid)
		require.NoError(t, err)
		require.True(t, exists)
	})
}
