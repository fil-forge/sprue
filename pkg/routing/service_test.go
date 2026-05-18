package routing_test

import (
	"net/url"
	"testing"

	"github.com/fil-forge/libforge/capabilities/blob"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/routing"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	spmemory "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func addProvider(t *testing.T, store *spmemory.Store, weight int, replicationWeight *int) storageprovider.Record {
	t.Helper()
	ctx := t.Context()
	storageProvider := testutil.RandomSigner(t)
	endpoint := testutil.Must(url.Parse("https://piri.example.com"))(t)
	err := store.Put(ctx, storageProvider.DID(), *endpoint, weight, replicationWeight)
	require.NoError(t, err)
	rec, err := store.Get(ctx, storageProvider.DID())
	require.NoError(t, err)
	return rec
}

func TestGetProviderInfo(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	t.Run("found", func(t *testing.T) {
		store := spmemory.New()
		rec := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, logger)

		info, err := svc.GetProviderInfo(ctx, rec.Provider)
		require.NoError(t, err)
		require.Equal(t, rec.Provider, info.ID)
		require.Equal(t, rec.Endpoint, info.Endpoint)
	})

	t.Run("not found", func(t *testing.T) {
		store := spmemory.New()
		svc := routing.NewService(store, logger)
		unknown := testutil.RandomSigner(t)

		_, err := svc.GetProviderInfo(ctx, unknown.DID())
		require.ErrorIs(t, err, storageprovider.ErrStorageProviderNotFound)
	})
}

func TestSelectStorageProvider(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()
	blob := blob.Blob{Size: 1024}

	t.Run("no providers", func(t *testing.T) {
		store := spmemory.New()
		svc := routing.NewService(store, logger)

		_, err := svc.SelectStorageProvider(ctx, blob)
		require.ErrorIs(t, err, routing.ErrCandidateUnavailable)
	})

	t.Run("single provider", func(t *testing.T) {
		store := spmemory.New()
		rec := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, logger)

		info, err := svc.SelectStorageProvider(ctx, blob)
		require.NoError(t, err)
		require.Equal(t, rec.Provider, info.ID)
	})

	t.Run("excludes zero weight providers", func(t *testing.T) {
		store := spmemory.New()
		addProvider(t, store, 0, nil)
		svc := routing.NewService(store, logger)

		_, err := svc.SelectStorageProvider(ctx, blob)
		require.ErrorIs(t, err, routing.ErrCandidateUnavailable)
	})

	t.Run("with exclusions", func(t *testing.T) {
		store := spmemory.New()
		excluded := addProvider(t, store, 100, nil)
		kept := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, logger)

		// Run multiple times to verify excluded provider is never selected
		for range 20 {
			info, err := svc.SelectStorageProvider(ctx, blob, routing.WithExclusions(excluded.Provider))
			require.NoError(t, err)
			require.Equal(t, kept.Provider, info.ID)
		}
	})

	t.Run("all providers excluded", func(t *testing.T) {
		store := spmemory.New()
		p := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, logger)

		_, err := svc.SelectStorageProvider(ctx, blob, routing.WithExclusions(p.Provider))
		require.ErrorIs(t, err, routing.ErrCandidateUnavailable)
	})

	t.Run("selects from multiple providers", func(t *testing.T) {
		store := spmemory.New()
		p1 := addProvider(t, store, 100, nil)
		p2 := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, logger)

		seen := map[string]bool{}
		for range 100 {
			info, err := svc.SelectStorageProvider(ctx, blob)
			require.NoError(t, err)
			seen[info.ID.String()] = true
		}
		// With equal weights over 100 iterations, both should be selected
		require.True(t, seen[p1.Provider.String()])
		require.True(t, seen[p2.Provider.String()])
	})
}

func TestSelectReplicationProvider(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()
	blob := blob.Blob{Size: 1024}

	t.Run("excludes primary", func(t *testing.T) {
		store := spmemory.New()
		primary := addProvider(t, store, 100, nil)
		secondary := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, logger)

		for range 20 {
			info, err := svc.SelectReplicationProvider(ctx, primary.Provider, blob)
			require.NoError(t, err)
			require.Equal(t, secondary.Provider, info.ID)
		}
	})

	t.Run("no candidates after excluding primary", func(t *testing.T) {
		store := spmemory.New()
		primary := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, logger)

		_, err := svc.SelectReplicationProvider(ctx, primary.Provider, blob)
		require.ErrorIs(t, err, routing.ErrCandidateUnavailable)
	})

	t.Run("excludes zero replication weight", func(t *testing.T) {
		store := spmemory.New()
		primary := addProvider(t, store, 100, nil)
		zeroRW := 0
		addProvider(t, store, 100, &zeroRW)
		good := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, logger)

		for range 20 {
			info, err := svc.SelectReplicationProvider(ctx, primary.Provider, blob)
			require.NoError(t, err)
			require.Equal(t, good.Provider, info.ID)
		}
	})

	t.Run("uses replication weight when set", func(t *testing.T) {
		store := spmemory.New()
		primary := addProvider(t, store, 100, nil)
		rw := 50
		replica := addProvider(t, store, 100, &rw)
		svc := routing.NewService(store, logger)

		info, err := svc.SelectReplicationProvider(ctx, primary.Provider, blob)
		require.NoError(t, err)
		require.Equal(t, replica.Provider, info.ID)
	})

	t.Run("combines primary exclusion with additional exclusions", func(t *testing.T) {
		store := spmemory.New()
		primary := addProvider(t, store, 100, nil)
		excluded := addProvider(t, store, 100, nil)
		keeper := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, logger)

		for range 20 {
			info, err := svc.SelectReplicationProvider(ctx, primary.Provider, blob, routing.WithExclusions(excluded.Provider))
			require.NoError(t, err)
			require.Equal(t, keeper.Provider, info.ID)
		}
	})
}
