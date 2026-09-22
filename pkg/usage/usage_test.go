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
		s.diffs,
		s.metrics,
		zaptest.NewLogger(t),
		usage.WithClock(func() time.Time { return now }),
	)
}

func provider() did.DID { return testutil.WebService.DID() }

func providers() []did.DID { return []did.DID{provider()} }

// run returns the one provider's samples from a series, asserting that is all
// the series holds.
func run(t *testing.T, series usage.Series) []usage.Sample {
	t.Helper()
	require.Len(t, series.Samples, 1)
	return series.Samples[provider()]
}

// seedAdd records a stored blob the way blob_registry.Register commits it: for
// each provider, a diff row and the matching counters together.
func seedAdd(t *testing.T, s stores, space did.DID, size uint64, at time.Time, providers ...did.DID) {
	t.Helper()
	seedChange(t, s, space, int64(size), at, map[string]uint64{
		metrics.BlobAddTotalMetric:     1,
		metrics.BlobAddSizeTotalMetric: size,
	}, providers...)
}

// seedRemove mirrors blob_registry.Deregister: a negative diff, and the remove
// counters rather than a decrement of the add counters.
func seedRemove(t *testing.T, s stores, space did.DID, size uint64, at time.Time, providers ...did.DID) {
	t.Helper()
	seedChange(t, s, space, -int64(size), at, map[string]uint64{
		metrics.BlobRemoveTotalMetric:     1,
		metrics.BlobRemoveSizeTotalMetric: size,
	}, providers...)
}

// seedChange applies one change to every provider given, defaulting to the one
// provider most cases use.
func seedChange(t *testing.T, s stores, space did.DID, delta int64, at time.Time, inc map[string]uint64, providers ...did.DID) {
	t.Helper()
	if len(providers) == 0 {
		providers = []did.DID{provider()}
	}
	cause := testutil.RandomCID(t)
	for _, p := range providers {
		require.NoError(t, s.diffs.Put(t.Context(), p, space, "sub", cause, delta, at))
		require.NoError(t, s.metrics.IncrementTotals(t.Context(), p, space, inc))
	}
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
func requireDense(t *testing.T, series usage.Series, samples []usage.Sample) {
	t.Helper()
	for i, s := range samples {
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

				series, err := svc.Sample(t.Context(), providers(), testutil.RandomDID(t), base, to, window)
				require.NoError(t, err)
				require.Len(t, run(t, series), 6)
				require.Equal(t, []uint64{0, 0, 0, 0, 0, 0}, stored(run(t, series)))
				require.Equal(t, []uint64{0, 0, 0, 0, 0, 0}, ingested(run(t, series)))
				requireDense(t, series, run(t, series))
			})

			t.Run("carries bytes stored before the range through all of it", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 1024, base.Add(-48*time.Hour))

				to := base.Add(3 * window)
				series, err := newService(t, s, to).Sample(t.Context(), providers(), space, base, to, window)
				require.NoError(t, err)
				require.Equal(t, []uint64{1024, 1024, 1024}, stored(run(t, series)))
				// Ingest happened before the range, so no bucket claims it.
				require.Equal(t, []uint64{0, 0, 0}, ingested(run(t, series)))
			})

			t.Run("steps up on a store and down on a removal", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 1024, base.Add(2*window+30*time.Minute))
				seedRemove(t, s, space, 1024, base.Add(5*window+30*time.Minute))

				to := base.Add(6 * window)
				series, err := newService(t, s, to).Sample(t.Context(), providers(), space, base, to, window)
				require.NoError(t, err)
				require.Equal(t, []uint64{0, 0, 1024, 1024, 1024, 0}, stored(run(t, series)))
				// A removal is not negative ingest: it leaves the flow alone.
				require.Equal(t, []uint64{0, 0, 1024, 0, 0, 0}, ingested(run(t, series)))
			})

			t.Run("sums several changes in one bucket", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				for _, m := range []int{10, 20, 30} {
					seedAdd(t, s, space, 100, base.Add(window+time.Duration(m)*time.Minute))
				}

				to := base.Add(3 * window)
				series, err := newService(t, s, to).Sample(t.Context(), providers(), space, base, to, window)
				require.NoError(t, err)
				require.Equal(t, []uint64{0, 300, 300}, stored(run(t, series)))
				require.Equal(t, []uint64{0, 300, 0}, ingested(run(t, series)))
			})

			t.Run("excludes changes made after the range", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 1024, base.Add(2*window+30*time.Minute))
				// Committed after the range but before now, so it is in the
				// counters and must be shed before the walk back begins.
				seedAdd(t, s, space, 4096, base.Add(7*window))

				to := base.Add(6 * window)
				series, err := newService(t, s, to).Sample(t.Context(), providers(), space, base, to, window+0)
				require.NoError(t, err)
				require.Equal(t, []uint64{0, 0, 1024, 1024, 1024, 1024}, stored(run(t, series)))
				require.Equal(t, []uint64{0, 0, 1024, 0, 0, 0}, ingested(run(t, series)))
			})

			t.Run("clamps a range that runs past now", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 512, base.Add(30*time.Minute))

				now := base.Add(3*window + 30*time.Minute)
				series, err := newService(t, s, now).Sample(t.Context(), providers(), space, base, base.Add(6*window), window)
				require.NoError(t, err)
				require.True(t, series.To.Equal(now), "series ends at %s, want %s", series.To, now)
				require.Len(t, run(t, series), 4)
				require.True(t, run(t, series)[3].End.Equal(now))
				require.Equal(t, []uint64{512, 512, 512, 512}, stored(run(t, series)))
				requireDense(t, series, run(t, series))
			})

			t.Run("returns no samples for a range entirely in the future", func(t *testing.T) {
				s := makeStores(t, k)
				now := base
				from := base.Add(window)
				series, err := newService(t, s, now).Sample(
					t.Context(), providers(), testutil.RandomDID(t), from, base.Add(2*window), window)
				require.NoError(t, err)
				require.Empty(t, run(t, series))
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
					t.Context(), providers(), space, base, base.Add(2*window), window)
				require.NoError(t, err)

				// Both buckets are returned; the second is short and ends now.
				require.Len(t, run(t, series), 2)
				require.True(t, run(t, series)[1].End.Equal(now))
				// A short bucket's reading is exact, and its ingest counts only
				// what has landed so far.
				require.Equal(t, []uint64{300, 1000}, stored(run(t, series)))
				require.Equal(t, []uint64{300, 700}, ingested(run(t, series)))
				requireDense(t, series, run(t, series))
			})

			t.Run("ends a short final bucket at the range end", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 64, base.Add(10*time.Minute))

				to := base.Add(2*window + 30*time.Minute)
				series, err := newService(t, s, to).Sample(t.Context(), providers(), space, base, to, window)
				require.NoError(t, err)
				require.Len(t, run(t, series), 3)
				require.True(t, run(t, series)[2].End.Equal(to))
				require.Equal(t, []uint64{64, 64, 64}, stored(run(t, series)))
				requireDense(t, series, run(t, series))
			})

			t.Run("counts a change on a bucket boundary in the bucket it opens", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 2048, base.Add(2*window))

				to := base.Add(4 * window)
				series, err := newService(t, s, to).Sample(t.Context(), providers(), space, base, to, window)
				require.NoError(t, err)
				// The bucket ending at 2h closed before the change, so it does
				// not hold it; the bucket that opens there does.
				require.Equal(t, []uint64{0, 0, 2048, 2048}, stored(run(t, series)))
				require.Equal(t, []uint64{0, 0, 2048, 0}, ingested(run(t, series)))
			})

			t.Run("counts a change at the range start in the first bucket", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				seedAdd(t, s, space, 777, base)

				to := base.Add(2 * window)
				series, err := newService(t, s, to).Sample(t.Context(), providers(), space, base, to, window)
				require.NoError(t, err)
				require.Equal(t, []uint64{777, 777}, stored(run(t, series)))
				require.Equal(t, []uint64{777, 0}, ingested(run(t, series)))
			})

			t.Run("ignores what another provider recorded", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				other := testutil.RandomDID(t)
				at := base.Add(window + 30*time.Minute)
				seedAdd(t, s, space, 1024, at)
				// A change only the other provider saw. Its rows and its
				// counters both sit under its own key, so neither reaches here.
				seedAdd(t, s, space, 9999, at, other)

				to := base.Add(3 * window)
				series, err := newService(t, s, to).Sample(t.Context(), providers(), space, base, to, window)
				require.NoError(t, err)
				require.Equal(t, []uint64{0, 1024, 1024}, stored(run(t, series)))
				require.Equal(t, []uint64{0, 1024, 0}, ingested(run(t, series)))
			})

			t.Run("reads a provider added after the space held blobs", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				late := testutil.RandomDID(t)

				// The original provider saw a change before the late one existed.
				seedAdd(t, s, space, 1024, base.Add(30*time.Minute))
				// The late provider only ever saw this one.
				seedAdd(t, s, space, 512, base.Add(window+30*time.Minute), late)

				to := base.Add(3 * window)
				series, err := newService(t, s, to).Sample(
					t.Context(), []did.DID{provider(), late}, space, base, to, window)
				require.NoError(t, err)

				// Each provider reports what it holds, and the late one reports
				// nothing before it arrived rather than a baseline it never stored.
				require.Equal(t, []uint64{1024, 1024, 1024}, stored(series.Samples[provider()]))
				require.Equal(t, []uint64{0, 512, 512}, stored(series.Samples[late]))
			})

			t.Run("returns a run per provider on one shared grid", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				second := testutil.RandomDID(t)
				at := base.Add(window + 30*time.Minute)

				// Both providers served the space from the start, so each one's
				// rows balance its own counters.
				seedAdd(t, s, space, 1024, at, provider(), second)

				to := base.Add(3 * window)
				series, err := newService(t, s, to).Sample(
					t.Context(), []did.DID{provider(), second}, space, base, to, window)
				require.NoError(t, err)

				require.Len(t, series.Samples, 2)
				for _, p := range []did.DID{provider(), second} {
					require.Equal(t, []uint64{0, 1024, 1024}, stored(series.Samples[p]), "provider %s", p)
					require.Equal(t, []uint64{0, 1024, 0}, ingested(series.Samples[p]), "provider %s", p)
					requireDense(t, series, series.Samples[p])
				}
			})

			t.Run("returns 768 samples for 32 days of hourly buckets", func(t *testing.T) {
				s := makeStores(t, k)
				space := testutil.RandomDID(t)
				now := base
				from := base.Add(-32 * 24 * time.Hour)
				seedAdd(t, s, space, 4096, from.Add(90*time.Minute))

				series, err := newService(t, s, now).Sample(t.Context(), providers(), space, from, now, window)
				require.NoError(t, err)
				require.Len(t, run(t, series), 768)
				requireDense(t, series, run(t, series))
				require.Equal(t, uint64(0), run(t, series)[0].BytesStored)
				require.Equal(t, uint64(4096), run(t, series)[767].BytesStored)
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
			_, err := svc.Sample(t.Context(), providers(), space, tc.from, tc.to, tc.window)
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

	_, err := svc.Sample(t.Context(), providers(), testutil.RandomDID(t), base, to, window)
	var named errors.Named
	require.ErrorAs(t, err, &named)
	require.Equal(t, "TooManySamples", named.Name())

	// One fewer bucket is served.
	series, err := svc.Sample(t.Context(), providers(), testutil.RandomDID(t), base, to.Add(-window), window)
	require.NoError(t, err)
	require.Len(t, run(t, series), usage.MaxSamples)
}

// ParseRange keeps the handler from asking for a span this wide, but Sample is
// exported and must refuse it rather than panic on a negative bucket count.
func TestSampleRejectsASpanThatSaturatesADuration(t *testing.T) {
	s := stores{diffs: spacediffmemory.New(), metrics: metricsmemory.NewSpaceStore()}
	now := time.Unix(1<<40, 0).UTC()
	svc := newService(t, s, now)

	_, err := svc.Sample(t.Context(), providers(), testutil.RandomDID(t),
		time.Unix(-1<<62, 0).UTC(), now, time.Second)

	var named errors.Named
	require.ErrorAs(t, err, &named)
	require.Equal(t, "TooManySamples", named.Name())
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

func (m *movingSpaceStore) Get(ctx context.Context, provider did.DID, space did.DID) (map[string]uint64, error) {
	m.reads++
	if m.reads <= m.settleAfter {
		if err := m.SpaceStore.IncrementTotals(ctx, provider, space, map[string]uint64{
			metrics.BlobAddTotalMetric:     1,
			metrics.BlobAddSizeTotalMetric: 512,
		}); err != nil {
			return nil, err
		}
	}
	return m.SpaceStore.Get(ctx, provider, space)
}

func TestSampleRefusesAnInconsistentRead(t *testing.T) {
	space := testutil.RandomDID(t)
	to := base.Add(2 * window)

	t.Run("fails when the space never settles", func(t *testing.T) {
		s := stores{
			diffs:   spacediffmemory.New(),
			metrics: &movingSpaceStore{SpaceStore: metricsmemory.NewSpaceStore(), settleAfter: 100},
		}
		_, err := newService(t, s, to).Sample(t.Context(), providers(), space, base, to, window)
		var named errors.Named
		require.ErrorAs(t, err, &named)
		require.Equal(t, "UsageUnstable", named.Name())
	})

	t.Run("succeeds once the space settles", func(t *testing.T) {
		s := stores{
			diffs:   spacediffmemory.New(),
			metrics: &movingSpaceStore{SpaceStore: metricsmemory.NewSpaceStore(), settleAfter: 1},
		}
		series, err := newService(t, s, to).Sample(t.Context(), providers(), space, base, to, window)
		require.NoError(t, err)
		require.Len(t, run(t, series), 2)
	})
}
