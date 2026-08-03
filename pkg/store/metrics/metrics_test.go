package metrics_test

import (
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	metricsmemory "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	metricspostgres "github.com/fil-forge/sprue/pkg/store/metrics/postgres"
	"github.com/stretchr/testify/require"
)

type StoreKind string

const (
	Memory   StoreKind = "memory"
	Postgres StoreKind = "postgres"
)

var storeKinds = []StoreKind{Memory, Postgres}

func makeStore(t *testing.T, k StoreKind) metrics.Store {
	switch k {
	case Memory:
		return metricsmemory.New()
	case Postgres:
		return createPostgresStore(t)
	}
	panic("unknown store kind")
}

func makeSpaceStore(t *testing.T, k StoreKind) metrics.SpaceStore {
	switch k {
	case Memory:
		return metricsmemory.NewSpaceStore()
	case Postgres:
		return createPostgresSpaceStore(t)
	}
	panic("unknown store kind")
}

func createPostgresStore(t *testing.T) *metricspostgres.Store {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	return metricspostgres.New(pool)
}

func createPostgresSpaceStore(t *testing.T) *metricspostgres.SpaceStore {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	return metricspostgres.NewSpaceStore(pool)
}

func TestStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			t.Run("returns empty metrics when nothing stored", func(t *testing.T) {
				s := makeStore(t, k)

				result, err := s.Get(t.Context())
				require.NoError(t, err)
				require.Empty(t, result)
			})

			t.Run("increments totals", func(t *testing.T) {
				s := makeStore(t, k)

				err := s.IncrementTotals(t.Context(), map[string]uint64{
					metrics.BlobAddTotalMetric:     3,
					metrics.BlobAddSizeTotalMetric: 1024,
				})
				require.NoError(t, err)

				result, err := s.Get(t.Context())
				require.NoError(t, err)
				require.Equal(t, uint64(3), result[metrics.BlobAddTotalMetric])
				require.Equal(t, uint64(1024), result[metrics.BlobAddSizeTotalMetric])
			})

			t.Run("accumulates increments across multiple calls", func(t *testing.T) {
				s := makeStore(t, k)

				err := s.IncrementTotals(t.Context(), map[string]uint64{
					metrics.BlobAddTotalMetric: 2,
				})
				require.NoError(t, err)

				err = s.IncrementTotals(t.Context(), map[string]uint64{
					metrics.BlobAddTotalMetric: 5,
				})
				require.NoError(t, err)

				result, err := s.Get(t.Context())
				require.NoError(t, err)
				require.Equal(t, uint64(7), result[metrics.BlobAddTotalMetric])
			})

			t.Run("increments multiple metrics independently", func(t *testing.T) {
				s := makeStore(t, k)

				err := s.IncrementTotals(t.Context(), map[string]uint64{
					metrics.BlobAddTotalMetric:        1,
					metrics.BlobRemoveTotalMetric:     2,
					metrics.UploadAddTotalMetric:      3,
					metrics.UploadRemoveTotalMetric:   4,
					metrics.BlobAddSizeTotalMetric:    512,
					metrics.BlobRemoveSizeTotalMetric: 256,
				})
				require.NoError(t, err)

				result, err := s.Get(t.Context())
				require.NoError(t, err)
				require.Equal(t, uint64(1), result[metrics.BlobAddTotalMetric])
				require.Equal(t, uint64(2), result[metrics.BlobRemoveTotalMetric])
				require.Equal(t, uint64(3), result[metrics.UploadAddTotalMetric])
				require.Equal(t, uint64(4), result[metrics.UploadRemoveTotalMetric])
				require.Equal(t, uint64(512), result[metrics.BlobAddSizeTotalMetric])
				require.Equal(t, uint64(256), result[metrics.BlobRemoveSizeTotalMetric])
			})
		})
	}
}

func TestSpaceStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			t.Run("returns empty metrics for unknown space", func(t *testing.T) {
				s := makeSpaceStore(t, k)
				space := testutil.RandomDID(t)

				result, err := s.Get(t.Context(), space)
				require.NoError(t, err)
				require.Empty(t, result)
			})

			t.Run("increments totals for a space", func(t *testing.T) {
				s := makeSpaceStore(t, k)
				space := testutil.RandomDID(t)

				err := s.IncrementTotals(t.Context(), space, map[string]uint64{
					metrics.BlobAddTotalMetric:     3,
					metrics.BlobAddSizeTotalMetric: 2048,
				})
				require.NoError(t, err)

				result, err := s.Get(t.Context(), space)
				require.NoError(t, err)
				require.Equal(t, uint64(3), result[metrics.BlobAddTotalMetric])
				require.Equal(t, uint64(2048), result[metrics.BlobAddSizeTotalMetric])
			})

			t.Run("accumulates increments across multiple calls for a space", func(t *testing.T) {
				s := makeSpaceStore(t, k)
				space := testutil.RandomDID(t)

				err := s.IncrementTotals(t.Context(), space, map[string]uint64{
					metrics.BlobAddTotalMetric: 4,
				})
				require.NoError(t, err)

				err = s.IncrementTotals(t.Context(), space, map[string]uint64{
					metrics.BlobAddTotalMetric: 6,
				})
				require.NoError(t, err)

				result, err := s.Get(t.Context(), space)
				require.NoError(t, err)
				require.Equal(t, uint64(10), result[metrics.BlobAddTotalMetric])
			})

			t.Run("isolates metrics between spaces", func(t *testing.T) {
				s := makeSpaceStore(t, k)
				space1 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)

				err := s.IncrementTotals(t.Context(), space1, map[string]uint64{
					metrics.BlobAddTotalMetric:     10,
					metrics.BlobAddSizeTotalMetric: 4096,
				})
				require.NoError(t, err)

				err = s.IncrementTotals(t.Context(), space2, map[string]uint64{
					metrics.BlobAddTotalMetric:     3,
					metrics.BlobAddSizeTotalMetric: 512,
				})
				require.NoError(t, err)

				result1, err := s.Get(t.Context(), space1)
				require.NoError(t, err)
				require.Equal(t, uint64(10), result1[metrics.BlobAddTotalMetric])
				require.Equal(t, uint64(4096), result1[metrics.BlobAddSizeTotalMetric])

				result2, err := s.Get(t.Context(), space2)
				require.NoError(t, err)
				require.Equal(t, uint64(3), result2[metrics.BlobAddTotalMetric])
				require.Equal(t, uint64(512), result2[metrics.BlobAddSizeTotalMetric])
			})

			t.Run("increments multiple metrics independently for a space", func(t *testing.T) {
				s := makeSpaceStore(t, k)
				space := testutil.RandomDID(t)

				err := s.IncrementTotals(t.Context(), space, map[string]uint64{
					metrics.BlobAddTotalMetric:        1,
					metrics.BlobRemoveTotalMetric:     2,
					metrics.UploadAddTotalMetric:      3,
					metrics.UploadRemoveTotalMetric:   4,
					metrics.BlobAddSizeTotalMetric:    512,
					metrics.BlobRemoveSizeTotalMetric: 256,
				})
				require.NoError(t, err)

				result, err := s.Get(t.Context(), space)
				require.NoError(t, err)
				require.Equal(t, uint64(1), result[metrics.BlobAddTotalMetric])
				require.Equal(t, uint64(2), result[metrics.BlobRemoveTotalMetric])
				require.Equal(t, uint64(3), result[metrics.UploadAddTotalMetric])
				require.Equal(t, uint64(4), result[metrics.UploadRemoveTotalMetric])
				require.Equal(t, uint64(512), result[metrics.BlobAddSizeTotalMetric])
				require.Equal(t, uint64(256), result[metrics.BlobRemoveSizeTotalMetric])
			})
		})
	}
}
