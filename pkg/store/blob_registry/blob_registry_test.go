package blobregistry_test

import (
	"context"
	"runtime"
	"testing"

	captypes "github.com/fil-forge/go-libstoracha/capabilities/types"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	blobregistryaws "github.com/fil-forge/sprue/pkg/store/blob_registry/aws"
	"github.com/fil-forge/sprue/pkg/store/blob_registry/memory"
	blobregistrypostgres "github.com/fil-forge/sprue/pkg/store/blob_registry/postgres"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	consumeraws "github.com/fil-forge/sprue/pkg/store/consumer/aws"
	memoryconsumer "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	consumerpostgres "github.com/fil-forge/sprue/pkg/store/consumer/postgres"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	metricsaws "github.com/fil-forge/sprue/pkg/store/metrics/aws"
	memorymetrics "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	metricspostgres "github.com/fil-forge/sprue/pkg/store/metrics/postgres"
	spacediffaws "github.com/fil-forge/sprue/pkg/store/space_diff/aws"
	memoryspacediff "github.com/fil-forge/sprue/pkg/store/space_diff/memory"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type StoreKind string

const (
	Memory   StoreKind = "memory"
	AWS      StoreKind = "aws"
	Postgres StoreKind = "postgres"
)

var storeKinds = []StoreKind{Memory, AWS, Postgres}

// storeBundle groups the blob registry with the dependency stores that tests
// need to set up state (e.g. adding consumers before registering blobs).
type storeBundle struct {
	registry     blobregistry.Store
	consumers    consumer.Store
	spaceMetrics metrics.SpaceStore
	adminMetrics metrics.Store
}

func makeStores(t *testing.T, k StoreKind) storeBundle {
	switch k {
	case Memory:
		consumerStore := memoryconsumer.New()
		spaceDiffStore := memoryspacediff.New()
		spaceMetrics := memorymetrics.NewSpaceStore()
		adminMetrics := memorymetrics.New()
		registry := memory.New(spaceDiffStore, consumerStore, spaceMetrics, adminMetrics)
		return storeBundle{
			registry:     registry,
			consumers:    consumerStore,
			spaceMetrics: spaceMetrics,
			adminMetrics: adminMetrics,
		}
	case AWS:
		return createAWSStores(t)
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
	spaceMetrics := metricspostgres.NewSpaceStore(pool)
	adminMetrics := metricspostgres.New(pool)
	registry := blobregistrypostgres.New(pool, consumerStore)
	return storeBundle{
		registry:     registry,
		consumers:    consumerStore,
		spaceMetrics: spaceMetrics,
		adminMetrics: adminMetrics,
	}
}

func createAWSStores(t *testing.T) storeBundle {
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

	dynamoEndpoint := testutil.CreateDynamo(t)
	dynamo := testutil.NewDynamoClient(t, dynamoEndpoint)

	suffix := uuid.NewString()

	consumerStore := consumeraws.New(dynamo, "consumer-"+suffix)
	require.NoError(t, consumerStore.Initialize(t.Context()))

	spaceDiffStore := spacediffaws.New(dynamo, "space-diff-"+suffix)
	require.NoError(t, spaceDiffStore.Initialize(t.Context()))

	spaceMetrics := metricsaws.NewSpaceStore(dynamo, "space-metrics-"+suffix)
	require.NoError(t, spaceMetrics.Initialize(t.Context()))

	adminMetrics := metricsaws.New(dynamo, "admin-metrics-"+suffix)
	require.NoError(t, adminMetrics.Initialize(t.Context()))

	registry := blobregistryaws.New(dynamo, "blob-registry-"+suffix, consumerStore, spaceDiffStore, spaceMetrics, adminMetrics)
	require.NoError(t, registry.Initialize(t.Context()))

	return storeBundle{
		registry:     registry,
		consumers:    consumerStore,
		spaceMetrics: spaceMetrics,
		adminMetrics: adminMetrics,
	}
}

// randomBlob returns a blob with a random digest and the given size.
func randomBlob(t *testing.T, size uint64) captypes.Blob {
	t.Helper()
	return captypes.Blob{Digest: testutil.RandomMultihash(t), Size: size}
}

func TestBlobRegistryStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			t.Run("registers a blob", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				require.NoError(t, b.consumers.Add(t.Context(), provider, space, customer, "sub1", cause))

				bl := randomBlob(t, 1024)
				require.NoError(t, b.registry.Register(t.Context(), space, bl, cause))

				rec, err := b.registry.Get(t.Context(), space, bl.Digest)
				require.NoError(t, err)
				require.Equal(t, space, rec.Space)
				require.Equal(t, bl.Digest, rec.Blob.Digest)
				require.Equal(t, bl.Size, rec.Blob.Size)
				require.Equal(t, cause, rec.Cause)
				require.False(t, rec.InsertedAt.IsZero())
			})

			t.Run("returns ErrEntryExists when registering a duplicate", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				require.NoError(t, b.consumers.Add(t.Context(), provider, space, customer, "sub1", cause))

				bl := randomBlob(t, 512)
				require.NoError(t, b.registry.Register(t.Context(), space, bl, cause))

				err := b.registry.Register(t.Context(), space, bl, cause)
				require.ErrorIs(t, err, blobregistry.ErrEntryExists)
			})

			t.Run("registers multiple blobs in the same space", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				require.NoError(t, b.consumers.Add(t.Context(), provider, space, customer, "sub1", cause))

				bl1 := randomBlob(t, 512)
				bl2 := randomBlob(t, 1024)
				require.NoError(t, b.registry.Register(t.Context(), space, bl1, cause))
				require.NoError(t, b.registry.Register(t.Context(), space, bl2, cause))

				rec1, err := b.registry.Get(t.Context(), space, bl1.Digest)
				require.NoError(t, err)
				require.Equal(t, bl1.Digest, rec1.Blob.Digest)

				rec2, err := b.registry.Get(t.Context(), space, bl2.Digest)
				require.NoError(t, err)
				require.Equal(t, bl2.Digest, rec2.Blob.Digest)
			})

			t.Run("Get returns ErrEntryNotFound for unknown blob", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)

				_, err := b.registry.Get(t.Context(), space, testutil.RandomMultihash(t))
				require.ErrorIs(t, err, blobregistry.ErrEntryNotFound)
			})

			t.Run("Get isolates blobs between spaces", func(t *testing.T) {
				b := makeStores(t, k)
				space1 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				require.NoError(t, b.consumers.Add(t.Context(), provider, space1, customer, "sub1", cause))
				require.NoError(t, b.consumers.Add(t.Context(), provider, space2, customer, "sub2", cause))

				bl := randomBlob(t, 1024)
				require.NoError(t, b.registry.Register(t.Context(), space1, bl, cause))

				_, err := b.registry.Get(t.Context(), space2, bl.Digest)
				require.ErrorIs(t, err, blobregistry.ErrEntryNotFound)
			})

			t.Run("deregisters a blob", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				require.NoError(t, b.consumers.Add(t.Context(), provider, space, customer, "sub1", cause))

				bl := randomBlob(t, 2048)
				require.NoError(t, b.registry.Register(t.Context(), space, bl, cause))

				require.NoError(t, b.registry.Deregister(t.Context(), space, bl.Digest, cause))

				_, err := b.registry.Get(t.Context(), space, bl.Digest)
				require.ErrorIs(t, err, blobregistry.ErrEntryNotFound)
			})

			t.Run("Deregister returns ErrEntryNotFound for unknown blob", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)

				err := b.registry.Deregister(t.Context(), space, testutil.RandomMultihash(t), testutil.RandomCID(t))
				require.ErrorIs(t, err, blobregistry.ErrEntryNotFound)
			})

			t.Run("lists blobs for a space", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				require.NoError(t, b.consumers.Add(t.Context(), provider, space, customer, "sub1", cause))

				for range 3 {
					require.NoError(t, b.registry.Register(t.Context(), space, randomBlob(t, 512), cause))
				}

				page, err := b.registry.List(t.Context(), space)
				require.NoError(t, err)
				require.Len(t, page.Results, 3)
			})

			t.Run("List returns empty page for unknown space", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)

				page, err := b.registry.List(t.Context(), space)
				require.NoError(t, err)
				require.Empty(t, page.Results)
				require.Nil(t, page.Cursor)
			})

			t.Run("List paginates results", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				require.NoError(t, b.consumers.Add(t.Context(), provider, space, customer, "sub1", cause))

				for range 5 {
					require.NoError(t, b.registry.Register(t.Context(), space, randomBlob(t, 512), cause))
				}

				all, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[blobregistry.Record], error) {
					listOpts := []blobregistry.ListOption{blobregistry.WithListLimit(2)}
					if opts.Cursor != nil {
						listOpts = append(listOpts, blobregistry.WithListCursor(*opts.Cursor))
					}
					return b.registry.List(ctx, space, listOpts...)
				})
				require.NoError(t, err)
				require.Len(t, all, 5)
			})

			t.Run("List isolates blobs between spaces", func(t *testing.T) {
				b := makeStores(t, k)
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				space1 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)
				require.NoError(t, b.consumers.Add(t.Context(), provider, space1, customer, "sub1", cause))
				require.NoError(t, b.consumers.Add(t.Context(), provider, space2, customer, "sub2", cause))

				require.NoError(t, b.registry.Register(t.Context(), space1, randomBlob(t, 512), cause))
				require.NoError(t, b.registry.Register(t.Context(), space1, randomBlob(t, 512), cause))
				require.NoError(t, b.registry.Register(t.Context(), space2, randomBlob(t, 512), cause))

				page1, err := b.registry.List(t.Context(), space1)
				require.NoError(t, err)
				require.Len(t, page1.Results, 2)

				page2, err := b.registry.List(t.Context(), space2)
				require.NoError(t, err)
				require.Len(t, page2.Results, 1)
			})

			t.Run("Register increments space and admin metrics", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				require.NoError(t, b.consumers.Add(t.Context(), provider, space, customer, "sub1", cause))

				bl := randomBlob(t, 8192)
				require.NoError(t, b.registry.Register(t.Context(), space, bl, cause))

				spaceM, err := b.spaceMetrics.Get(t.Context(), space)
				require.NoError(t, err)
				require.Equal(t, uint64(1), spaceM[metrics.BlobAddTotalMetric])
				require.Equal(t, uint64(8192), spaceM[metrics.BlobAddSizeTotalMetric])

				adminM, err := b.adminMetrics.Get(t.Context())
				require.NoError(t, err)
				require.Equal(t, uint64(1), adminM[metrics.BlobAddTotalMetric])
				require.Equal(t, uint64(8192), adminM[metrics.BlobAddSizeTotalMetric])
			})

			t.Run("Deregister decrements space and admin metrics", func(t *testing.T) {
				b := makeStores(t, k)
				space := testutil.RandomDID(t)
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				require.NoError(t, b.consumers.Add(t.Context(), provider, space, customer, "sub1", cause))

				bl := randomBlob(t, 4096)
				require.NoError(t, b.registry.Register(t.Context(), space, bl, cause))
				require.NoError(t, b.registry.Deregister(t.Context(), space, bl.Digest, cause))

				spaceM, err := b.spaceMetrics.Get(t.Context(), space)
				require.NoError(t, err)
				require.Equal(t, uint64(1), spaceM[metrics.BlobRemoveTotalMetric])
				require.Equal(t, uint64(4096), spaceM[metrics.BlobRemoveSizeTotalMetric])

				adminM, err := b.adminMetrics.Get(t.Context())
				require.NoError(t, err)
				require.Equal(t, uint64(1), adminM[metrics.BlobRemoveTotalMetric])
				require.Equal(t, uint64(4096), adminM[metrics.BlobRemoveSizeTotalMetric])
			})
		})
	}
}
