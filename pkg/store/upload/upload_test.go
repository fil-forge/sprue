package upload_test

import (
	"time"

	"context"
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	consumermemory "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	consumerpostgres "github.com/fil-forge/sprue/pkg/store/consumer/postgres"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	metricsmemory "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	metricspostgres "github.com/fil-forge/sprue/pkg/store/metrics/postgres"
	"github.com/fil-forge/sprue/pkg/store/upload"
	uploadmemory "github.com/fil-forge/sprue/pkg/store/upload/memory"
	uploadpostgres "github.com/fil-forge/sprue/pkg/store/upload/postgres"
	uploaddiff "github.com/fil-forge/sprue/pkg/store/upload_diff"
	uploaddiffmemory "github.com/fil-forge/sprue/pkg/store/upload_diff/memory"
	uploaddiffpostgres "github.com/fil-forge/sprue/pkg/store/upload_diff/postgres"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
)

type StoreKind string

const (
	Memory   StoreKind = "memory"
	Postgres StoreKind = "postgres"
)

var storeKinds = []StoreKind{Memory, Postgres}

// manyShards is a shard count large enough that listing spans multiple pages.
// It preserves the value of the removed AWS store's ShardThreshold so the
// "many shards" cases keep exercising the same scale.
const manyShards = 5000

// storeBundle groups the upload store with the dependency stores that tests
// need to set up state (a space must be provisioned before an upload can be
// recorded against it) and to assert on the object-count metrics.
type storeBundle struct {
	uploads      upload.Store
	consumers    consumer.Store
	uploadDiffs  uploaddiff.Store
	spaceMetrics metrics.SpaceStore
	adminMetrics metrics.Store
}

// provision gives space a consumer, so the upload store has a
// provider/subscription pair to key its diff rows by. Returns the provider,
// which listing the diffs requires.
func (b storeBundle) provision(t *testing.T, space did.DID) did.DID {
	t.Helper()
	provider := testutil.RandomDID(t)
	customer := testutil.RandomDID(t)
	require.NoError(t, b.consumers.Add(t.Context(), provider, space, customer, "sub1", testutil.RandomCID(t)))
	return provider
}

// adminTotal reads one global counter. The bundle is shared across the
// subtests of a backend, so admin assertions are made against a baseline read
// before the operation rather than an absolute value.
func (b storeBundle) adminTotal(t *testing.T, metric string) uint64 {
	t.Helper()
	m, err := b.adminMetrics.Get(t.Context())
	require.NoError(t, err)
	return m[metric]
}

func makeStores(t *testing.T, k StoreKind) storeBundle {
	switch k {
	case Memory:
		consumerStore := consumermemory.New()
		uploadDiffStore := uploaddiffmemory.New()
		spaceMetrics := metricsmemory.NewSpaceStore()
		adminMetrics := metricsmemory.New()
		return storeBundle{
			uploads:      uploadmemory.New(uploadDiffStore, consumerStore, spaceMetrics, adminMetrics),
			consumers:    consumerStore,
			uploadDiffs:  uploadDiffStore,
			spaceMetrics: spaceMetrics,
			adminMetrics: adminMetrics,
		}
	case Postgres:
		return createPostgresStores(t)
	}
	panic("unknown store kind")
}

func createPostgresStores(t *testing.T) storeBundle {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	consumerStore := consumerpostgres.New(pool)
	return storeBundle{
		uploads:      uploadpostgres.New(pool, consumerStore),
		consumers:    consumerStore,
		uploadDiffs:  uploaddiffpostgres.New(pool),
		spaceMetrics: metricspostgres.NewSpaceStore(pool),
		adminMetrics: metricspostgres.New(pool),
	}
}

// listAllShards collects all shards for an upload by paginating in batches of 1000.
func listAllShards(t *testing.T, uploadStore upload.Store, space did.DID, root cid.Cid) []cid.Cid {
	t.Helper()
	shards, err := store.Collect(t.Context(), func(ctx context.Context, options store.PaginationConfig) (store.Page[cid.Cid], error) {
		opts := []upload.ListShardsOption{upload.WithListShardsLimit(1000)}
		if options.Cursor != nil {
			opts = append(opts, upload.WithListShardsCursor(*options.Cursor))
		}
		return uploadStore.ListShards(ctx, space, root, opts...)
	})
	require.NoError(t, err)
	return shards
}

func TestUploadStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			b := makeStores(t, k)
			store := b.uploads
			t.Run("adds an upload", func(t *testing.T) {
				space := testutil.RandomDID(t)
				b.provision(t, space)
				root := testutil.RandomCID(t)
				index := testutil.RandomCID(t)
				shards := []cid.Cid{testutil.RandomCID(t), testutil.RandomCID(t)}
				cause := testutil.RandomCID(t)

				err := store.Upsert(t.Context(), space, root, &index, shards, cause)
				require.NoError(t, err)

				exists, err := store.Exists(t.Context(), space, root)
				require.NoError(t, err)
				require.True(t, exists)

				record, err := store.Get(t.Context(), space, root)
				require.NoError(t, err)
				require.Equal(t, space, record.Space)
				require.Equal(t, root, record.Root)
				require.Equal(t, cause, record.Cause)
			})

			t.Run("lists uploads", func(t *testing.T) {
				space := testutil.RandomDID(t)
				b.provision(t, space)
				roots := []cid.Cid{testutil.RandomCID(t), testutil.RandomCID(t), testutil.RandomCID(t)}
				indexes := []cid.Cid{testutil.RandomCID(t), testutil.RandomCID(t), testutil.RandomCID(t)}
				cause := testutil.RandomCID(t)

				for i, root := range roots {
					err := store.Upsert(t.Context(), space, root, &indexes[i], nil, cause)
					require.NoError(t, err)
				}

				// list all uploads
				page, err := store.List(t.Context(), space)
				require.NoError(t, err)
				require.Len(t, page.Results, 3)

				// list with a limit of 2 - should return first 2 and a cursor
				limit := 2
				page, err = store.List(t.Context(), space, upload.WithListLimit(limit))
				require.NoError(t, err)
				require.Len(t, page.Results, 2)
				require.NotNil(t, page.Cursor)

				// use cursor to fetch the next page
				page, err = store.List(t.Context(), space, upload.WithListCursor(*page.Cursor))
				require.NoError(t, err)
				require.Len(t, page.Results, 1)
			})

			t.Run("updates an upload", func(t *testing.T) {
				space := testutil.RandomDID(t)
				b.provision(t, space)
				root := testutil.RandomCID(t)
				index := testutil.RandomCID(t)
				cause := testutil.RandomCID(t)

				initialShards := make([]cid.Cid, 3)
				for i := range initialShards {
					initialShards[i] = testutil.RandomCID(t)
				}

				err := store.Upsert(t.Context(), space, root, nil, initialShards, cause)
				require.NoError(t, err)

				// build a second batch of shards that includes one duplicate from the
				// first batch and enough new shards to push the total over manyShards
				newShardCount := manyShards - len(initialShards) + 2 // +2 to exceed threshold, accounting for the duplicate
				additionalShards := make([]cid.Cid, newShardCount)
				additionalShards[0] = initialShards[0] // duplicate
				for i := 1; i < newShardCount; i++ {
					additionalShards[i] = testutil.RandomCID(t)
				}

				newCause := testutil.RandomCID(t)
				err = store.Upsert(t.Context(), space, root, &index, additionalShards, newCause)
				require.NoError(t, err)

				// cause should be updated
				record, err := store.Get(t.Context(), space, root)
				require.NoError(t, err)
				require.Equal(t, newCause, record.Cause)

				// total unique shards = initialShards + additionalShards - 1 duplicate
				wantShards := len(initialShards) + newShardCount - 1
				require.Greater(t, wantShards, manyShards)

				allShards := listAllShards(t, store, space, root)
				require.Len(t, allShards, wantShards)
			})

			t.Run("inspects an upload", func(t *testing.T) {
				root := testutil.RandomCID(t)
				index := testutil.RandomCID(t)
				cause := testutil.RandomCID(t)

				// inspecting a root not in any space returns empty spaces
				record, err := store.Inspect(t.Context(), root)
				require.NoError(t, err)
				require.Empty(t, record.Spaces)

				// upsert the root into two different spaces
				space1 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)
				b.provision(t, space1)
				b.provision(t, space2)
				require.NoError(t, store.Upsert(t.Context(), space1, root, &index, nil, cause))
				require.NoError(t, store.Upsert(t.Context(), space2, root, &index, nil, cause))

				record, err = store.Inspect(t.Context(), root)
				require.NoError(t, err)
				require.Len(t, record.Spaces, 2)
				require.ElementsMatch(t, []any{space1, space2}, []any{record.Spaces[0], record.Spaces[1]})
			})

			t.Run("removes an upload", func(t *testing.T) {
				cases := []struct {
					name       string
					shardCount int
				}{
					{"few shards", 3},
					{"many shards", manyShards + 1},
				}
				for _, tc := range cases {
					t.Run(tc.name, func(t *testing.T) {
						space := testutil.RandomDID(t)
						b.provision(t, space)
						b.provision(t, space)
						root := testutil.RandomCID(t)
						index := testutil.RandomCID(t)
						cause := testutil.RandomCID(t)

						// removing a non-existent upload returns an error
						err := store.Remove(t.Context(), space, root, cause)
						require.ErrorIs(t, err, upload.ErrUploadNotFound)

						shards := make([]cid.Cid, tc.shardCount)
						for i := range shards {
							shards[i] = testutil.RandomCID(t)
						}

						err = store.Upsert(t.Context(), space, root, &index, shards, cause)
						require.NoError(t, err)

						err = store.Remove(t.Context(), space, root, cause)
						require.NoError(t, err)

						exists, err := store.Exists(t.Context(), space, root)
						require.NoError(t, err)
						require.False(t, exists)

						// shards should also be gone
						allShards := listAllShards(t, store, space, root)
						require.Empty(t, allShards)

						// removing again returns an error
						err = store.Remove(t.Context(), space, root, cause)
						require.ErrorIs(t, err, upload.ErrUploadNotFound)
					})
				}
			})

			t.Run("lists shards of an upload", func(t *testing.T) {
				cases := []struct {
					name       string
					shardCount int
				}{
					{"few shards", 3},
					{"many shards", manyShards + 1},
				}
				for _, tc := range cases {
					t.Run(tc.name, func(t *testing.T) {
						space := testutil.RandomDID(t)
						b.provision(t, space)
						b.provision(t, space)
						root := testutil.RandomCID(t)
						index := testutil.RandomCID(t)
						cause := testutil.RandomCID(t)

						shards := make([]cid.Cid, tc.shardCount)
						for i := range shards {
							shards[i] = testutil.RandomCID(t)
						}

						err := store.Upsert(t.Context(), space, root, &index, shards, cause)
						require.NoError(t, err)

						// list with a limit of 2 - should return first 2 and a cursor
						page, err := store.ListShards(t.Context(), space, root, upload.WithListShardsLimit(2))
						require.NoError(t, err)
						require.Len(t, page.Results, 2)
						require.NotNil(t, page.Cursor)

						// list all shards via pagination helper
						allShards := listAllShards(t, store, space, root)
						require.Len(t, allShards, tc.shardCount)
					})
				}
			})

			t.Run("Upsert increments space and admin object counts", func(t *testing.T) {
				space := testutil.RandomDID(t)
				provider := b.provision(t, space)
				root := testutil.RandomCID(t)
				cause := testutil.RandomCID(t)
				adminBefore := b.adminTotal(t, metrics.UploadAddTotalMetric)

				require.NoError(t, store.Upsert(t.Context(), space, root, nil, nil, cause))

				spaceM, err := b.spaceMetrics.Get(t.Context(), space)
				require.NoError(t, err)
				require.Equal(t, uint64(1), spaceM[metrics.UploadAddTotalMetric])
				require.Equal(t, adminBefore+1, b.adminTotal(t, metrics.UploadAddTotalMetric))

				diffs, err := b.uploadDiffs.List(t.Context(), provider, space, time.Time{})
				require.NoError(t, err)
				require.Len(t, diffs.Results, 1)
				require.Equal(t, int64(1), diffs.Results[0].Delta)
				require.Equal(t, cause, diffs.Results[0].Cause)
			})

			t.Run("re-adding the same root does not count again", func(t *testing.T) {
				space := testutil.RandomDID(t)
				provider := b.provision(t, space)
				root := testutil.RandomCID(t)
				index := testutil.RandomCID(t)

				require.NoError(t, store.Upsert(t.Context(), space, root, nil, nil, testutil.RandomCID(t)))
				// The capability is an upsert: a client retry merges shards and
				// replaces the index, and must leave the count where it was.
				require.NoError(t, store.Upsert(t.Context(), space, root, &index, []cid.Cid{testutil.RandomCID(t)}, testutil.RandomCID(t)))

				spaceM, err := b.spaceMetrics.Get(t.Context(), space)
				require.NoError(t, err)
				require.Equal(t, uint64(1), spaceM[metrics.UploadAddTotalMetric])

				diffs, err := b.uploadDiffs.List(t.Context(), provider, space, time.Time{})
				require.NoError(t, err)
				require.Len(t, diffs.Results, 1)
			})

			t.Run("Remove increments the remove counters", func(t *testing.T) {
				space := testutil.RandomDID(t)
				provider := b.provision(t, space)
				root := testutil.RandomCID(t)
				addCause := testutil.RandomCID(t)
				removeCause := testutil.RandomCID(t)
				adminBefore := b.adminTotal(t, metrics.UploadRemoveTotalMetric)

				require.NoError(t, store.Upsert(t.Context(), space, root, nil, nil, addCause))
				require.NoError(t, store.Remove(t.Context(), space, root, removeCause))

				spaceM, err := b.spaceMetrics.Get(t.Context(), space)
				require.NoError(t, err)
				require.Equal(t, uint64(1), spaceM[metrics.UploadAddTotalMetric])
				require.Equal(t, uint64(1), spaceM[metrics.UploadRemoveTotalMetric])
				require.Equal(t, adminBefore+1, b.adminTotal(t, metrics.UploadRemoveTotalMetric))

				// The diff log nets to zero: one object added, one removed.
				diffs, err := b.uploadDiffs.List(t.Context(), provider, space, time.Time{})
				require.NoError(t, err)
				require.Len(t, diffs.Results, 2)
				var net int64
				for _, d := range diffs.Results {
					net += d.Delta
				}
				require.Zero(t, net)
			})

			t.Run("removing an unknown root counts nothing", func(t *testing.T) {
				space := testutil.RandomDID(t)
				provider := b.provision(t, space)
				cause := testutil.RandomCID(t)

				err := store.Remove(t.Context(), space, testutil.RandomCID(t), cause)
				require.ErrorIs(t, err, upload.ErrUploadNotFound)

				spaceM, err := b.spaceMetrics.Get(t.Context(), space)
				require.NoError(t, err)
				require.Zero(t, spaceM[metrics.UploadRemoveTotalMetric])

				diffs, err := b.uploadDiffs.List(t.Context(), provider, space, time.Time{})
				require.NoError(t, err)
				require.Empty(t, diffs.Results)
			})
		})
	}
}
