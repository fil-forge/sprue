package usage_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	metricsmemory "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	metricspostgres "github.com/fil-forge/sprue/pkg/store/metrics/postgres"
	spacediff "github.com/fil-forge/sprue/pkg/store/space_diff"
	spacediffmemory "github.com/fil-forge/sprue/pkg/store/space_diff/memory"
	spacediffpostgres "github.com/fil-forge/sprue/pkg/store/space_diff/postgres"
	"github.com/fil-forge/sprue/pkg/usage"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type StoreKind string

const (
	Memory   StoreKind = "memory"
	Postgres StoreKind = "postgres"
)

var storeKinds = []StoreKind{Memory, Postgres}

// base is an arbitrary fixed instant. Every case works in whole hours from it
// so the bucket a change lands in is obvious from the test.
var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const window = time.Hour

type stores struct {
	diffs   spacediff.Store
	metrics metrics.SpaceStore
}

func makeStores(t *testing.T, k StoreKind) stores {
	switch k {
	case Memory:
		return stores{diffs: spacediffmemory.New(), metrics: metricsmemory.NewSpaceStore()}
	case Postgres:
		if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
			if !testutil.IsDockerAvailable(t) {
				t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
			}
		}
		if !testutil.IsDockerAvailable(t) {
			t.SkipNow()
		}
		pool := testutil.CreatePostgres(t)
		return stores{diffs: spacediffpostgres.New(pool), metrics: metricspostgres.NewSpaceStore(pool)}
	}
	panic("unknown store kind")
}

func newService(t *testing.T, s stores, now time.Time) *usage.Service {
	t.Helper()
	return usage.NewService(
		testutil.WebService,
		s.diffs,
		s.metrics,
		zaptest.NewLogger(t),
		usage.WithClock(func() time.Time { return now }),
	)
}

func provider() did.DID { return testutil.WebService.DID() }

// seedAdd records a stored blob the way blob_registry.Register commits it: one
// diff row against the provider, and the matching counters.
func seedAdd(t *testing.T, s stores, space did.DID, size uint64, at time.Time) {
	t.Helper()
	seedDiff(t, s, provider(), space, int64(size), at)
	require.NoError(t, s.metrics.IncrementTotals(t.Context(), space, map[string]uint64{
		metrics.BlobAddTotalMetric:     1,
		metrics.BlobAddSizeTotalMetric: size,
	}))
}

// seedRemove mirrors blob_registry.Deregister: a negative diff, and the remove
// counters rather than a decrement of the add counters.
func seedRemove(t *testing.T, s stores, space did.DID, size uint64, at time.Time) {
	t.Helper()
	seedDiff(t, s, provider(), space, -int64(size), at)
	require.NoError(t, s.metrics.IncrementTotals(t.Context(), space, map[string]uint64{
		metrics.BlobRemoveTotalMetric:     1,
		metrics.BlobRemoveSizeTotalMetric: size,
	}))
}

// seedDiff writes only the ledger row, with no counter movement. A space
// provided for by two providers gets a row each per change, but the counters
// move once.
func seedDiff(t *testing.T, s stores, provider, space did.DID, delta int64, at time.Time) {
	t.Helper()
	require.NoError(t, s.diffs.Put(t.Context(), provider, space, "sub", testutil.RandomCID(t), delta, at))
}

func stored(samples []usage.Sample) []uint64 {
	out := make([]uint64, 0, len(samples))
	for _, s := range samples {
		out = append(out, s.BytesStored)
	}
	return out
}

func ingested(samples []usage.Sample) []uint64 {
	out := make([]uint64, 0, len(samples))
	for _, s := range samples {
		out = append(out, s.BytesIngested)
	}
	return out
}

// requireDense asserts the series covers every window end in order with no
// gaps, which is what consumers averaging the series depend on.
func requireDense(t *testing.T, series usage.Series) {
	t.Helper()
	for i, s := range series.Samples {
		want := series.From.Add(time.Duration(i+1) * series.Window)
		if want.After(series.To) {
			want = series.To
		}
		require.True(t, s.End.Equal(want), "sample %d ends at %s, want %s", i, s.End, want)
	}
}

func TestSample(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			t.Run("returns zeros for a space it has never seen", func(t *testing.T) {
				s := makeStores(t, k)
				to := base.Add(6 * window)
				svc := newService(t, s, to)

				series, err := svc.Sample(t.Context(), testutil.RandomDID(t), base, to, window)
				require.NoError(t, err)
				require.Len(t, series.Samples, 6)
				require.Equal(t, []uint64{0, 0, 0, 0, 0, 0}, stored(series.Samples))
				require.Equal(t, []uint64{0, 0, 0, 0, 0, 0}, ingested(series.Samples))
				requireDense(t, series)
			})

			t.Run("carries bytes stored before the range through all of it", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 1024, base.Add(-48*time.Hour))

				to := base.Add(3 * window)
				series, err := newService(t, s, to).Sample(t.Context(), space, base, to, window)
				require.NoError(t, err)
				require.Equal(t, []uint64{1024, 1024, 1024}, stored(series.Samples))
				// Ingest happened before the range, so no bucket claims it.
				require.Equal(t, []uint64{0, 0, 0}, ingested(series.Samples))
			})

			t.Run("steps up on a store and down on a removal", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 1024, base.Add(2*window+30*time.Minute))
				seedRemove(t, s, space, 1024, base.Add(5*window+30*time.Minute))

				to := base.Add(6 * window)
				series, err := newService(t, s, to).Sample(t.Context(), space, base, to, window)
				require.NoError(t, err)
				require.Equal(t, []uint64{0, 0, 1024, 1024, 1024, 0}, stored(series.Samples))
				// A removal is not negative ingest: it leaves the flow alone.
				require.Equal(t, []uint64{0, 0, 1024, 0, 0, 0}, ingested(series.Samples))
			})

			t.Run("sums several changes in one bucket", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				for _, m := range []int{10, 20, 30} {
					seedAdd(t, s, space, 100, base.Add(window+time.Duration(m)*time.Minute))
				}

				to := base.Add(3 * window)
				series, err := newService(t, s, to).Sample(t.Context(), space, base, to, window)
				require.NoError(t, err)
				require.Equal(t, []uint64{0, 300, 300}, stored(series.Samples))
				require.Equal(t, []uint64{0, 300, 0}, ingested(series.Samples))
			})

			t.Run("excludes changes made after the range", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 1024, base.Add(2*window+30*time.Minute))
				// Committed after the range but before now, so it is in the
				// counters and must be shed before the walk back begins.
				seedAdd(t, s, space, 4096, base.Add(7*window))

				to := base.Add(6 * window)
				series, err := newService(t, s, to).Sample(t.Context(), space, base, to, window+0)
				require.NoError(t, err)
				require.Equal(t, []uint64{0, 0, 1024, 1024, 1024, 1024}, stored(series.Samples))
				require.Equal(t, []uint64{0, 0, 1024, 0, 0, 0}, ingested(series.Samples))
			})

			t.Run("clamps a range that runs past now", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 512, base.Add(30*time.Minute))

				now := base.Add(3*window + 30*time.Minute)
				series, err := newService(t, s, now).Sample(t.Context(), space, base, base.Add(6*window), window)
				require.NoError(t, err)
				require.True(t, series.To.Equal(now), "series ends at %s, want %s", series.To, now)
				require.Len(t, series.Samples, 4)
				require.True(t, series.Samples[3].End.Equal(now))
				require.Equal(t, []uint64{512, 512, 512, 512}, stored(series.Samples))
				requireDense(t, series)
			})

			t.Run("returns no samples for a range entirely in the future", func(t *testing.T) {
				s := makeStores(t, k)
				now := base
				from := base.Add(window)
				series, err := newService(t, s, now).Sample(
					t.Context(), testutil.RandomDID(t), from, base.Add(2*window), window)
				require.NoError(t, err)
				require.Empty(t, series.Samples)
				// The range covers nothing, but it still has to read as a range:
				// an end before its start describes no interval at all.
				require.True(t, series.From.Equal(from), "series starts at %s, want %s", series.From, from)
				require.True(t, series.To.Equal(from), "series ends at %s, want %s", series.To, from)
			})

			t.Run("shortens the last bucket to the present", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 300, base.Add(20*time.Minute))
				seedAdd(t, s, space, 700, base.Add(window+10*time.Minute))

				// Half way through the second bucket of a two bucket range.
				now := base.Add(window + 30*time.Minute)
				series, err := newService(t, s, now).Sample(
					t.Context(), space, base, base.Add(2*window), window)
				require.NoError(t, err)

				// Both buckets are returned; the second is short and ends now.
				require.Len(t, series.Samples, 2)
				require.True(t, series.Samples[1].End.Equal(now))
				// A short bucket's reading is exact, and its ingest counts only
				// what has landed so far.
				require.Equal(t, []uint64{300, 1000}, stored(series.Samples))
				require.Equal(t, []uint64{300, 700}, ingested(series.Samples))
				requireDense(t, series)
			})

			t.Run("ends a short final bucket at the range end", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 64, base.Add(10*time.Minute))

				to := base.Add(2*window + 30*time.Minute)
				series, err := newService(t, s, to).Sample(t.Context(), space, base, to, window)
				require.NoError(t, err)
				require.Len(t, series.Samples, 3)
				require.True(t, series.Samples[2].End.Equal(to))
				require.Equal(t, []uint64{64, 64, 64}, stored(series.Samples))
				requireDense(t, series)
			})

			t.Run("counts a change on a bucket boundary in the bucket it opens", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 2048, base.Add(2*window))

				to := base.Add(4 * window)
				series, err := newService(t, s, to).Sample(t.Context(), space, base, to, window)
				require.NoError(t, err)
				// The bucket ending at 2h closed before the change, so it does
				// not hold it; the bucket that opens there does.
				require.Equal(t, []uint64{0, 0, 2048, 2048}, stored(series.Samples))
				require.Equal(t, []uint64{0, 0, 2048, 0}, ingested(series.Samples))
			})

			t.Run("counts a change at the range start in the first bucket", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 777, base)

				to := base.Add(2 * window)
				series, err := newService(t, s, to).Sample(t.Context(), space, base, to, window)
				require.NoError(t, err)
				require.Equal(t, []uint64{777, 777}, stored(series.Samples))
				require.Equal(t, []uint64{777, 0}, ingested(series.Samples))
			})

			t.Run("ignores rows recorded against another provider", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				other := testutil.RandomDID(t)
				at := base.Add(window + 30*time.Minute)
				seedAdd(t, s, space, 1024, at)
				// The same change as seen by a second provider: another row,
				// but the counters moved only once.
				seedDiff(t, s, other, space, 1024, at)

				to := base.Add(3 * window)
				series, err := newService(t, s, to).Sample(t.Context(), space, base, to, window)
				require.NoError(t, err)
				require.Equal(t, []uint64{0, 1024, 1024}, stored(series.Samples))
				require.Equal(t, []uint64{0, 1024, 0}, ingested(series.Samples))
			})

			t.Run("returns 768 samples for 32 days of hourly buckets", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				now := base
				from := base.Add(-32 * 24 * time.Hour)
				seedAdd(t, s, space, 4096, from.Add(90*time.Minute))

				series, err := newService(t, s, now).Sample(t.Context(), space, from, now, window)
				require.NoError(t, err)
				require.Len(t, series.Samples, 768)
				requireDense(t, series)
				require.Equal(t, uint64(0), series.Samples[0].BytesStored)
				require.Equal(t, uint64(4096), series.Samples[767].BytesStored)
			})
		})
	}
}

func TestSampleRejectsBadArguments(t *testing.T) {
	s := stores{diffs: spacediffmemory.New(), metrics: metricsmemory.NewSpaceStore()}
	svc := newService(t, s, base.Add(24*time.Hour))
	space := testutil.RandomDID(t)

	for _, tc := range []struct {
		name       string
		from, to   time.Time
		window     time.Duration
		wantFailed string
	}{
		{"empty range", base, base, window, "InvalidRange"},
		{"reversed range", base.Add(window), base, window, "InvalidRange"},
		{"zero window", base, base.Add(window), 0, "InvalidWindow"},
		{"negative window", base, base.Add(window), -window, "InvalidWindow"},
		{"window beyond a year", base, base.Add(window), 400 * 24 * time.Hour, "InvalidWindow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Sample(t.Context(), space, tc.from, tc.to, tc.window)
			var named errors.Named
			require.ErrorAs(t, err, &named)
			require.Equal(t, tc.wantFailed, named.Name())
		})
	}
}

func TestSampleRejectsMoreBucketsThanTheLimit(t *testing.T) {
	s := stores{diffs: spacediffmemory.New(), metrics: metricsmemory.NewSpaceStore()}
	to := base.Add((usage.MaxSamples + 1) * window)
	// The count is taken after the range is clamped to now, so the clock has to
	// be past the end for the whole range to count.
	svc := newService(t, s, to)

	_, err := svc.Sample(t.Context(), testutil.RandomDID(t), base, to, window)
	var named errors.Named
	require.ErrorAs(t, err, &named)
	require.Equal(t, "TooManySamples", named.Name())

	// One fewer bucket is served.
	series, err := svc.Sample(t.Context(), testutil.RandomDID(t), base, to.Add(-window), window)
	require.NoError(t, err)
	require.Len(t, series.Samples, usage.MaxSamples)
}

func TestParseRange(t *testing.T) {
	t.Run("converts seconds to a range", func(t *testing.T) {
		from, to, w, err := usage.ParseRange(1700000000, 1700003600, 3600)
		require.NoError(t, err)
		require.Equal(t, int64(1700000000), from.Unix())
		require.Equal(t, int64(1700003600), to.Unix())
		require.Equal(t, time.Hour, w)
	})

	for _, tc := range []struct {
		name             string
		from, to, window int64
		wantFailed       string
	}{
		{"zero from", 0, 1700003600, 3600, "InvalidRange"},
		{"negative from", -1, 1700003600, 3600, "InvalidRange"},
		{"zero to", 1700000000, 0, 3600, "InvalidRange"},
		{"reversed", 1700003600, 1700000000, 3600, "InvalidRange"},
		{"zero window", 1700000000, 1700003600, 0, "InvalidWindow"},
		// Large enough to overflow a time.Duration if it were not rejected.
		{"unrepresentable window", 1700000000, 1700003600, 1 << 40, "InvalidWindow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := usage.ParseRange(tc.from, tc.to, tc.window)
			var named errors.Named
			require.ErrorAs(t, err, &named)
			require.Equal(t, tc.wantFailed, named.Name())
		})
	}
}

// movingSpaceStore stands in for a space being written to while it is read. It
// advances the counters on each read until settleAfter reads have happened.
type movingSpaceStore struct {
	metrics.SpaceStore
	reads       int
	settleAfter int
}

func (m *movingSpaceStore) Get(ctx context.Context, space did.DID) (map[string]uint64, error) {
	m.reads++
	if m.reads <= m.settleAfter {
		if err := m.SpaceStore.IncrementTotals(ctx, space, map[string]uint64{
			metrics.BlobAddTotalMetric:     1,
			metrics.BlobAddSizeTotalMetric: 512,
		}); err != nil {
			return nil, err
		}
	}
	return m.SpaceStore.Get(ctx, space)
}

func TestSampleRefusesAnInconsistentRead(t *testing.T) {
	space := testutil.RandomDID(t)
	to := base.Add(2 * window)

	t.Run("fails when the space never settles", func(t *testing.T) {
		s := stores{
			diffs:   spacediffmemory.New(),
			metrics: &movingSpaceStore{SpaceStore: metricsmemory.NewSpaceStore(), settleAfter: 100},
		}
		_, err := newService(t, s, to).Sample(t.Context(), space, base, to, window)
		var named errors.Named
		require.ErrorAs(t, err, &named)
		require.Equal(t, "UsageUnstable", named.Name())
	})

	t.Run("succeeds once the space settles", func(t *testing.T) {
		s := stores{
			diffs:   spacediffmemory.New(),
			metrics: &movingSpaceStore{SpaceStore: metricsmemory.NewSpaceStore(), settleAfter: 1},
		}
		series, err := newService(t, s, to).Sample(t.Context(), space, base, to, window)
		require.NoError(t, err)
		require.Len(t, series.Samples, 2)
	})
}
