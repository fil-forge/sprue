package routing_test

import (
	"net/url"
	"testing"

	"github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/routing"
	routingpolicy "github.com/fil-forge/sprue/pkg/store/routing_policy"
	rpmemory "github.com/fil-forge/sprue/pkg/store/routing_policy/memory"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	spmemory "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func addProvider(t *testing.T, store *spmemory.Store, weight int, replicationWeight *int) storageprovider.Record {
	t.Helper()
	ctx := t.Context()
	storageProvider := testutil.RandomIssuer(t)
	endpoint := testutil.Must(url.Parse("https://piri.example.com"))(t)
	err := store.Put(ctx, storageProvider.DID(), *endpoint, weight, replicationWeight, container.New())
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
		svc := routing.NewService(store, rpmemory.New(), logger)

		info, err := svc.GetProviderInfo(ctx, rec.Provider)
		require.NoError(t, err)
		require.Equal(t, rec.Provider, info.ID)
		require.Equal(t, rec.Endpoint, info.Endpoint)
	})

	t.Run("not found", func(t *testing.T) {
		store := spmemory.New()
		svc := routing.NewService(store, rpmemory.New(), logger)
		unknown := testutil.RandomIssuer(t)

		_, err := svc.GetProviderInfo(ctx, unknown.DID())
		require.ErrorIs(t, err, storageprovider.ErrStorageProviderNotFound)
	})
}

func TestSelectStorageProvider(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()
	blob := blob.Blob{Size: 1024}
	space := testutil.RandomDID(t)

	t.Run("no providers", func(t *testing.T) {
		store := spmemory.New()
		svc := routing.NewService(store, rpmemory.New(), logger)

		_, err := svc.SelectStorageProvider(ctx, space, blob)
		require.ErrorIs(t, err, routing.ErrCandidateUnavailable)
	})

	t.Run("single provider", func(t *testing.T) {
		store := spmemory.New()
		rec := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, rpmemory.New(), logger)

		info, err := svc.SelectStorageProvider(ctx, space, blob)
		require.NoError(t, err)
		require.Equal(t, rec.Provider, info.ID)
	})

	t.Run("excludes zero weight providers", func(t *testing.T) {
		store := spmemory.New()
		addProvider(t, store, 0, nil)
		svc := routing.NewService(store, rpmemory.New(), logger)

		_, err := svc.SelectStorageProvider(ctx, space, blob)
		require.ErrorIs(t, err, routing.ErrCandidateUnavailable)
	})

	t.Run("with exclusions", func(t *testing.T) {
		store := spmemory.New()
		excluded := addProvider(t, store, 100, nil)
		kept := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, rpmemory.New(), logger)

		// Run multiple times to verify excluded provider is never selected
		for range 20 {
			info, err := svc.SelectStorageProvider(ctx, space, blob, routing.WithExclusions(excluded.Provider))
			require.NoError(t, err)
			require.Equal(t, kept.Provider, info.ID)
		}
	})

	t.Run("all providers excluded", func(t *testing.T) {
		store := spmemory.New()
		p := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, rpmemory.New(), logger)

		_, err := svc.SelectStorageProvider(ctx, space, blob, routing.WithExclusions(p.Provider))
		require.ErrorIs(t, err, routing.ErrCandidateUnavailable)
	})

	t.Run("selects from multiple providers", func(t *testing.T) {
		store := spmemory.New()
		p1 := addProvider(t, store, 100, nil)
		p2 := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, rpmemory.New(), logger)

		seen := map[string]bool{}
		for range 100 {
			info, err := svc.SelectStorageProvider(ctx, space, blob)
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
		svc := routing.NewService(store, rpmemory.New(), logger)

		for range 20 {
			info, err := svc.SelectReplicationProvider(ctx, primary.Provider, blob)
			require.NoError(t, err)
			require.Equal(t, secondary.Provider, info.ID)
		}
	})

	t.Run("no candidates after excluding primary", func(t *testing.T) {
		store := spmemory.New()
		primary := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, rpmemory.New(), logger)

		_, err := svc.SelectReplicationProvider(ctx, primary.Provider, blob)
		require.ErrorIs(t, err, routing.ErrCandidateUnavailable)
	})

	t.Run("excludes zero replication weight", func(t *testing.T) {
		store := spmemory.New()
		primary := addProvider(t, store, 100, nil)
		zeroRW := 0
		addProvider(t, store, 100, &zeroRW)
		good := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, rpmemory.New(), logger)

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
		svc := routing.NewService(store, rpmemory.New(), logger)

		info, err := svc.SelectReplicationProvider(ctx, primary.Provider, blob)
		require.NoError(t, err)
		require.Equal(t, replica.Provider, info.ID)
	})

	t.Run("combines primary exclusion with additional exclusions", func(t *testing.T) {
		store := spmemory.New()
		primary := addProvider(t, store, 100, nil)
		excluded := addProvider(t, store, 100, nil)
		keeper := addProvider(t, store, 100, nil)
		svc := routing.NewService(store, rpmemory.New(), logger)

		for range 20 {
			info, err := svc.SelectReplicationProvider(ctx, primary.Provider, blob, routing.WithExclusions(excluded.Provider))
			require.NoError(t, err)
			require.Equal(t, keeper.Provider, info.ID)
		}
	})
}

func TestSelectStorageProviderWithPolicy(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()
	blob := blob.Blob{Size: 1024}

	// setup registers two healthy providers and a policy naming only the first,
	// referenced by the returned space.
	setup := func(t *testing.T) (*routing.Service, *spmemory.Store, *rpmemory.Store, did.DID, storageprovider.Record, storageprovider.Record) {
		store := spmemory.New()
		policies := rpmemory.New()
		svc := routing.NewService(store, policies, logger)
		inPolicy := addProvider(t, store, 100, nil)
		outside := addProvider(t, store, 100, nil)
		policy := testutil.RandomDID(t)
		space := testutil.RandomDID(t)
		require.NoError(t, svc.PutPolicy(ctx, policy, []did.DID{inPolicy.Provider}, testutil.RandomCID(t)))
		require.NoError(t, svc.UseSpacePolicy(ctx, space, policy, testutil.RandomCID(t)))
		return svc, store, policies, space, inPolicy, outside
	}

	t.Run("restricts selection to policy candidates", func(t *testing.T) {
		svc, _, _, space, inPolicy, _ := setup(t)
		for range 20 {
			info, err := svc.SelectStorageProvider(ctx, space, blob)
			require.NoError(t, err)
			require.Equal(t, inPolicy.Provider, info.ID)
		}
	})

	t.Run("exclusions apply within the candidate set", func(t *testing.T) {
		svc, _, _, space, inPolicy, _ := setup(t)
		_, err := svc.SelectStorageProvider(ctx, space, blob, routing.WithExclusions(inPolicy.Provider))
		require.ErrorIs(t, err, routing.ErrCandidateUnavailable)
	})

	t.Run("zero weight candidate does not fall back to other providers", func(t *testing.T) {
		svc, store, _, space, inPolicy, _ := setup(t)
		require.NoError(t, store.Put(ctx, inPolicy.Provider, inPolicy.Endpoint, 0, nil, inPolicy.Proofs))
		_, err := svc.SelectStorageProvider(ctx, space, blob)
		require.ErrorIs(t, err, routing.ErrCandidateUnavailable)
	})

	t.Run("deregistered candidate does not fall back to other providers", func(t *testing.T) {
		svc, store, _, space, inPolicy, _ := setup(t)
		require.NoError(t, store.Delete(ctx, inPolicy.Provider))
		_, err := svc.SelectStorageProvider(ctx, space, blob)
		require.ErrorIs(t, err, routing.ErrCandidateUnavailable)
	})

	t.Run("space without a policy is unconstrained", func(t *testing.T) {
		svc, _, _, _, inPolicy, outside := setup(t)
		seen := map[did.DID]bool{}
		for range 50 {
			info, err := svc.SelectStorageProvider(ctx, testutil.RandomDID(t), blob)
			require.NoError(t, err)
			seen[info.ID] = true
		}
		require.True(t, seen[inPolicy.Provider])
		require.True(t, seen[outside.Provider])
	})

	t.Run("cleared policy returns space to default routing", func(t *testing.T) {
		svc, _, _, space, inPolicy, outside := setup(t)
		require.NoError(t, svc.ClearSpacePolicy(ctx, space))
		seen := map[did.DID]bool{}
		for range 50 {
			info, err := svc.SelectStorageProvider(ctx, space, blob)
			require.NoError(t, err)
			seen[info.ID] = true
		}
		require.True(t, seen[inPolicy.Provider])
		require.True(t, seen[outside.Provider])
	})
}

func TestPutPolicy(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	t.Run("rejects empty candidates", func(t *testing.T) {
		svc := routing.NewService(spmemory.New(), rpmemory.New(), logger)
		err := svc.PutPolicy(ctx, testutil.RandomDID(t), nil, testutil.RandomCID(t))
		var named errors.Named
		require.ErrorAs(t, err, &named)
		require.Equal(t, routing.InvalidCandidatesErrorName, named.Name())
	})

	t.Run("rejects unregistered candidate", func(t *testing.T) {
		store := spmemory.New()
		svc := routing.NewService(store, rpmemory.New(), logger)
		registered := addProvider(t, store, 100, nil)
		err := svc.PutPolicy(ctx, testutil.RandomDID(t), []did.DID{registered.Provider, testutil.RandomDID(t)}, testutil.RandomCID(t))
		var named errors.Named
		require.ErrorAs(t, err, &named)
		require.Equal(t, routing.InvalidCandidatesErrorName, named.Name())
	})

	t.Run("use requires a known policy", func(t *testing.T) {
		svc := routing.NewService(spmemory.New(), rpmemory.New(), logger)
		err := svc.UseSpacePolicy(ctx, testutil.RandomDID(t), testutil.RandomDID(t), testutil.RandomCID(t))
		require.ErrorIs(t, err, routingpolicy.ErrPolicyNotFound)
	})
}
