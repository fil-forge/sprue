package allocation_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/allocation"
	allocationmemory "github.com/fil-forge/sprue/pkg/store/allocation/memory"
	allocationpostgres "github.com/fil-forge/sprue/pkg/store/allocation/postgres"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	consumermemory "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	consumerpostgres "github.com/fil-forge/sprue/pkg/store/consumer/postgres"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	metricsmemory "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	metricspostgres "github.com/fil-forge/sprue/pkg/store/metrics/postgres"
	spacediff "github.com/fil-forge/sprue/pkg/store/space_diff"
	spacediffmemory "github.com/fil-forge/sprue/pkg/store/space_diff/memory"
	spacediffpostgres "github.com/fil-forge/sprue/pkg/store/space_diff/postgres"
	"github.com/fil-forge/ucantone/did"
	"github.com/stretchr/testify/require"
)

type StoreKind string

const (
	Memory   StoreKind = "memory"
	Postgres StoreKind = "postgres"
)

var storeKinds = []StoreKind{Memory, Postgres}

// storeBundle groups the allocation store with the dependency stores that
// tests need to set up state (consumers) and observe billing side effects
// (space diffs + metrics).
type storeBundle struct {
	allocations  allocation.Store
	consumers    consumer.Store
	spaceDiff    spacediff.Store
	spaceMetrics metrics.SpaceStore
	adminMetrics metrics.Store
}

func makeStores(t *testing.T, k StoreKind) storeBundle {
	switch k {
	case Memory:
		consumerStore := consumermemory.New()
		spaceDiffStore := spacediffmemory.New()
		spaceMetrics := metricsmemory.NewSpaceStore()
		adminMetrics := metricsmemory.New()
		allocations := allocationmemory.New(spaceDiffStore, consumerStore, spaceMetrics, adminMetrics)
		return storeBundle{
			allocations:  allocations,
			consumers:    consumerStore,
			spaceDiff:    spaceDiffStore,
			spaceMetrics: spaceMetrics,
			adminMetrics: adminMetrics,
		}
	case Postgres:
		return createPostgresStores(t)
	}
	panic("unknown store kind")
}

func createPostgresStores(t *testing.T) storeBundle {
	// This test expects docker to be running in linux CI environments and fails if it's not
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	// otherwise this test is running locally, skip it if docker isn't available
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	consumerStore := consumerpostgres.New(pool)
	spaceDiffStore := spacediffpostgres.New(pool)
	spaceMetrics := metricspostgres.NewSpaceStore(pool)
	adminMetrics := metricspostgres.New(pool)
	allocations := allocationpostgres.New(pool, consumerStore)
	return storeBundle{
		allocations:  allocations,
		consumers:    consumerStore,
		spaceDiff:    spaceDiffStore,
		spaceMetrics: spaceMetrics,
		adminMetrics: adminMetrics,
	}
}

// provisionSpace adds a consumer record so the allocation store's billing
// writes have a subscription to attribute diffs to. Returns the provider DID.
func provisionSpace(t *testing.T, b storeBundle, space did.DID) did.DID {
	t.Helper()
	provider := testutil.RandomDID(t)
	customer := testutil.RandomDID(t)
	require.NoError(t, b.consumers.Add(t.Context(), provider, space, customer, "sub1", testutil.RandomCID(t)))
	return provider
}

// collectDiffs pages through all space diffs for the provider + space.
func collectDiffs(t *testing.T, b storeBundle, provider did.DID, space did.DID) []spacediff.DifferenceRecord {
	t.Helper()
	diffs, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[spacediff.DifferenceRecord], error) {
		listOpts := []spacediff.ListOption{}
		if opts.Cursor != nil {
			listOpts = append(listOpts, spacediff.WithListCursor(*opts.Cursor))
		}
		return b.spaceDiff.List(ctx, provider, space, time.Time{}, listOpts...)
	})
	require.NoError(t, err)
	return diffs
}

// randomBlob returns a blob with a random digest and the given size.
func randomBlob(t *testing.T, size uint64) blob.Blob {
	t.Helper()
	return blob.Blob{Digest: testutil.RandomMultihash(t), Size: size}
}

func TestAllocationStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			t.Run("adds an allocation", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				provisionSpace(t, b, space)

				bl := randomBlob(t, 1024)
				require.NoError(t, b.allocations.Add(t.Context(), space, bl, cause))

				rec, err := b.allocations.Get(t.Context(), space, bl.Digest)
				require.NoError(t, err)
				require.Equal(t, space, rec.Space)
				require.Equal(t, bl.Digest, rec.Blob.Digest)
				require.Equal(t, bl.Size, rec.Blob.Size)
				require.Equal(t, cause, rec.Cause)
				require.False(t, rec.InsertedAt.IsZero())
			})

			t.Run("returns ErrEntryExists when adding a duplicate", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				provisionSpace(t, b, space)

				bl := randomBlob(t, 512)
				require.NoError(t, b.allocations.Add(t.Context(), space, bl, cause))

				err := b.allocations.Add(t.Context(), space, bl, cause)
				require.ErrorIs(t, err, allocation.ErrEntryExists)
			})

			t.Run("Add fails without a consumer for the space", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)

				err := b.allocations.Add(t.Context(), space, randomBlob(t, 512), testutil.RandomCID(t))
				require.ErrorIs(t, err, consumer.ErrConsumerNotFound)
			})

			t.Run("Get returns ErrEntryNotFound for unknown allocation", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)

				_, err := b.allocations.Get(t.Context(), space, testutil.RandomMultihash(t))
				require.ErrorIs(t, err, allocation.ErrEntryNotFound)
			})

			t.Run("Get isolates allocations between spaces", func(t *testing.T) {
				b := makeStores(t, k)
				space1 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				provisionSpace(t, b, space1)

				bl := randomBlob(t, 1024)
				require.NoError(t, b.allocations.Add(t.Context(), space1, bl, cause))

				_, err := b.allocations.Get(t.Context(), space2, bl.Digest)
				require.ErrorIs(t, err, allocation.ErrEntryNotFound)
			})

			t.Run("removes an allocation", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				provisionSpace(t, b, space)

				bl := randomBlob(t, 2048)
				require.NoError(t, b.allocations.Add(t.Context(), space, bl, cause))

				require.NoError(t, b.allocations.Remove(t.Context(), space, bl.Digest, testutil.RandomCID(t)))

				_, err := b.allocations.Get(t.Context(), space, bl.Digest)
				require.ErrorIs(t, err, allocation.ErrEntryNotFound)
			})

			t.Run("Remove returns ErrEntryNotFound for unknown allocation", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)

				err := b.allocations.Remove(t.Context(), space, testutil.RandomMultihash(t), testutil.RandomCID(t))
				require.ErrorIs(t, err, allocation.ErrEntryNotFound)
			})

			t.Run("adds an allocation again after removal", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				provisionSpace(t, b, space)

				bl := randomBlob(t, 512)
				require.NoError(t, b.allocations.Add(t.Context(), space, bl, cause))
				require.NoError(t, b.allocations.Remove(t.Context(), space, bl.Digest, testutil.RandomCID(t)))
				require.NoError(t, b.allocations.Add(t.Context(), space, bl, cause))

				rec, err := b.allocations.Get(t.Context(), space, bl.Digest)
				require.NoError(t, err)
				require.Equal(t, bl.Digest, rec.Blob.Digest)
			})

			t.Run("lists allocations for a space", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				provisionSpace(t, b, space)

				for range 3 {
					require.NoError(t, b.allocations.Add(t.Context(), space, randomBlob(t, 512), cause))
				}

				page, err := b.allocations.List(t.Context(), space)
				require.NoError(t, err)
				require.Len(t, page.Results, 3)
			})

			t.Run("List returns empty page for unknown space", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)

				page, err := b.allocations.List(t.Context(), space)
				require.NoError(t, err)
				require.Empty(t, page.Results)
				require.Nil(t, page.Cursor)
			})

			t.Run("List paginates results", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				provisionSpace(t, b, space)

				for range 5 {
					require.NoError(t, b.allocations.Add(t.Context(), space, randomBlob(t, 512), cause))
				}

				all, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[allocation.Record], error) {
					listOpts := []allocation.ListOption{allocation.WithListLimit(2)}
					if opts.Cursor != nil {
						listOpts = append(listOpts, allocation.WithListCursor(*opts.Cursor))
					}
					return b.allocations.List(ctx, space, listOpts...)
				})
				require.NoError(t, err)
				require.Len(t, all, 5)
			})

			t.Run("List isolates allocations between spaces", func(t *testing.T) {
				b := makeStores(t, k)
				space1 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				provisionSpace(t, b, space1)
				provisionSpace(t, b, space2)

				require.NoError(t, b.allocations.Add(t.Context(), space1, randomBlob(t, 512), cause))
				require.NoError(t, b.allocations.Add(t.Context(), space1, randomBlob(t, 512), cause))
				require.NoError(t, b.allocations.Add(t.Context(), space2, randomBlob(t, 512), cause))

				page1, err := b.allocations.List(t.Context(), space1)
				require.NoError(t, err)
				require.Len(t, page1.Results, 2)

				page2, err := b.allocations.List(t.Context(), space2)
				require.NoError(t, err)
				require.Len(t, page2.Results, 1)
			})

			t.Run("Add increments space and admin metrics", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				provisionSpace(t, b, space)

				bl := randomBlob(t, 8192)
				require.NoError(t, b.allocations.Add(t.Context(), space, bl, cause))

				spaceM, err := b.spaceMetrics.Get(t.Context(), space)
				require.NoError(t, err)
				require.Equal(t, uint64(1), spaceM[metrics.BlobAddTotalMetric])
				require.Equal(t, uint64(8192), spaceM[metrics.BlobAddSizeTotalMetric])

				adminM, err := b.adminMetrics.Get(t.Context())
				require.NoError(t, err)
				require.Equal(t, uint64(1), adminM[metrics.BlobAddTotalMetric])
				require.Equal(t, uint64(8192), adminM[metrics.BlobAddSizeTotalMetric])
			})

			t.Run("Remove increments space and admin metrics", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				provisionSpace(t, b, space)

				bl := randomBlob(t, 4096)
				require.NoError(t, b.allocations.Add(t.Context(), space, bl, cause))
				require.NoError(t, b.allocations.Remove(t.Context(), space, bl.Digest, testutil.RandomCID(t)))

				spaceM, err := b.spaceMetrics.Get(t.Context(), space)
				require.NoError(t, err)
				require.Equal(t, uint64(1), spaceM[metrics.BlobRemoveTotalMetric])
				require.Equal(t, uint64(4096), spaceM[metrics.BlobRemoveSizeTotalMetric])

				adminM, err := b.adminMetrics.Get(t.Context())
				require.NoError(t, err)
				require.Equal(t, uint64(1), adminM[metrics.BlobRemoveTotalMetric])
				require.Equal(t, uint64(4096), adminM[metrics.BlobRemoveSizeTotalMetric])
			})

			t.Run("Add writes a positive space diff", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				addCause := testutil.RandomCID(t)
				provider := provisionSpace(t, b, space)

				bl := randomBlob(t, 1024)
				require.NoError(t, b.allocations.Add(t.Context(), space, bl, addCause))

				diffs := collectDiffs(t, b, provider, space)
				require.Len(t, diffs, 1)
				require.Equal(t, int64(1024), diffs[0].Delta)
				require.Equal(t, addCause, diffs[0].Cause)
				require.Equal(t, "sub1", diffs[0].Subscription)
				require.Equal(t, provider, diffs[0].Provider)
				require.Equal(t, space, diffs[0].Space)
			})

			t.Run("Remove writes a negative space diff with its own cause", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				addCause := testutil.RandomCID(t)
				removeCause := testutil.RandomCID(t)
				provider := provisionSpace(t, b, space)

				bl := randomBlob(t, 2048)
				require.NoError(t, b.allocations.Add(t.Context(), space, bl, addCause))
				require.NoError(t, b.allocations.Remove(t.Context(), space, bl.Digest, removeCause))

				// Match by delta/cause rather than order: the memory space diff
				// store sorts by receipt time truncated to milliseconds, so
				// same-millisecond ordering is unstable.
				diffs := collectDiffs(t, b, provider, space)
				require.Len(t, diffs, 2)
				byCause := map[string]spacediff.DifferenceRecord{}
				for _, d := range diffs {
					byCause[d.Cause.String()] = d
				}
				require.Equal(t, int64(2048), byCause[addCause.String()].Delta)
				require.Equal(t, int64(-2048), byCause[removeCause.String()].Delta)
			})

			t.Run("Add writes a diff per provider", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				provider1 := provisionSpace(t, b, space)
				provider2 := provisionSpace(t, b, space)

				bl := randomBlob(t, 512)
				require.NoError(t, b.allocations.Add(t.Context(), space, bl, cause))

				diffs1 := collectDiffs(t, b, provider1, space)
				require.Len(t, diffs1, 1)
				require.Equal(t, int64(512), diffs1[0].Delta)

				diffs2 := collectDiffs(t, b, provider2, space)
				require.Len(t, diffs2, 1)
				require.Equal(t, int64(512), diffs2[0].Delta)
			})
		})
	}
}
