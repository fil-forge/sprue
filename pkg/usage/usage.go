// Package usage reconstructs per-space usage time series from the space diff
// log and the space's running byte counters.
package usage

import (
	"context"
	"fmt"
	"math"
	"time"

	metricscmds "github.com/fil-forge/libforge/commands/metrics"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	spacediff "github.com/fil-forge/sprue/pkg/store/space_diff"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"go.uber.org/zap"
)

const (
	// MaxSamples is the most samples one call returns. Consumers ask for up to
	// 768 (32 days at hourly granularity), and the CBOR and DAG-JSON codecs
	// both refuse arrays longer than 8192, so the ceiling is real.
	MaxSamples = 2048

	// maxWindow bounds the window before it becomes a [time.Duration], which
	// overflows silently past roughly 292 years.
	maxWindow = 366 * 24 * time.Hour

	// maxScanRows bounds the diff scan. One stored blob is one row, so without
	// a bound a range covering a busy period reads without limit.
	maxScanRows = 500_000

	// totalsAttempts is how many times to read the space counters before giving
	// up on a reading consistent with the diff scan.
	totalsAttempts = 3
)

var (
	// ErrInvalidRange is returned when the range is not a non-empty interval.
	ErrInvalidRange = errors.New(
		metricscmds.InvalidRangeErrorName,
		"range must be a non-empty interval between positive unix timestamps",
	)
	// ErrInvalidWindow is returned when the window is not a usable duration.
	ErrInvalidWindow = errors.New(
		metricscmds.InvalidWindowErrorName,
		"window must be positive and no longer than %s", maxWindow,
	)
	// ErrUsageUnstable is returned when the space was written to throughout the
	// read, so no consistent series could be assembled. It is retryable.
	ErrUsageUnstable = errors.New(
		metricscmds.UsageUnstableErrorName,
		"space usage changed throughout the read",
	)
	// ErrRangeTooBusy is returned when the range holds more recorded changes
	// than one call will scan.
	ErrRangeTooBusy = errors.New(
		metricscmds.RangeTooBusyErrorName,
		"range holds more than %d recorded changes, request a shorter range", maxScanRows,
	)
)

func errTooManySamples(n int) error {
	return errors.New(
		metricscmds.TooManySamplesErrorName,
		"%d samples requested, the limit is %d: widen the window or shorten the range", n, MaxSamples,
	)
}

// Sample is one bucket of a usage series.
type Sample struct {
	// End is the instant the bucket closes.
	End time.Time
	// BytesStored is the bytes the space holds as of End.
	BytesStored uint64
	// BytesIngested is the bytes added to the space during the bucket.
	BytesIngested uint64
}

// Series holds one dense run of samples per storage provider: one sample per
// window over [From, To), ordered by ascending End, with no gaps. Every
// provider's run shares the one bucket grid. To is the requested end clamped to
// the current time.
//
// The runs describe the same stored bytes from each provider's side rather than
// parts of a whole, so adding them together would count the same bytes once per
// provider.
type Series struct {
	From    time.Time
	To      time.Time
	Window  time.Duration
	Samples map[did.DID][]Sample
}

// Service answers usage queries against the recorded space diffs.
type Service struct {
	diffs        spacediff.Store
	spaceMetrics metrics.SpaceStore
	logger       *zap.Logger
	now          func() time.Time
}

type Option func(*Service)

// WithClock replaces the clock used to clamp a range to the present.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// NewService builds a usage service over the space diff log and the space byte
// counters.
func NewService(diffs spacediff.Store, spaceMetrics metrics.SpaceStore, logger *zap.Logger, opts ...Option) *Service {
	s := &Service{
		diffs:        diffs,
		spaceMetrics: spaceMetrics,
		logger:       logger,
		now:          time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ParseRange validates the arguments of a usage query, given as unix timestamps
// and a number of seconds. It may return [ErrInvalidRange] or
// [ErrInvalidWindow].
func ParseRange(from, to, window int64) (time.Time, time.Time, time.Duration, error) {
	if from <= 0 || to <= 0 || to <= from {
		return time.Time{}, time.Time{}, 0, ErrInvalidRange
	}
	if window <= 0 || window > int64(maxWindow/time.Second) {
		return time.Time{}, time.Time{}, 0, ErrInvalidWindow
	}
	return time.Unix(from, 0).UTC(), time.Unix(to, 0).UTC(), time.Duration(window) * time.Second, nil
}

// Sample returns the usage series for a space over [from, to) in buckets of
// window, ending no later than the present, one run per storage provider given.
// A space with nothing stored, and one this service has never seen, both return
// runs of zeros.
//
// The provider is part of the reading, not a detail of it. A change to a space
// writes a diff row and moves the running totals for each of the space's
// providers, so a provider's totals balance its own rows however long it has
// served the space. Reading several providers means replaying each separately.
// The range is clamped once, so every run shares one grid.
//
// The last bucket is short whenever window does not divide the range covered.
// Its stored bytes are exact, being a reading taken where it ends, while its
// ingested bytes cover only the elapsed part of the window.
//
// It may return [ErrInvalidRange], [ErrInvalidWindow], [ErrRangeTooBusy],
// [ErrUsageUnstable], or a TooManySamples failure when the range holds more
// buckets than [MaxSamples].
func (s *Service) Sample(ctx context.Context, providers []did.DID, space did.DID, from, to time.Time, window time.Duration) (Series, error) {
	if from.IsZero() || !to.After(from) {
		return Series{}, ErrInvalidRange
	}
	if window <= 0 || window > maxWindow {
		return Series{}, ErrInvalidWindow
	}

	// Report what has already happened. A range running past the present ends
	// at the present instead, which shortens its last bucket rather than
	// padding one out with the latest reading and pulling a consumer's average
	// forward. A range lying entirely ahead covers nothing at all, and must
	// still come back with an end no earlier than its start.
	if now := s.now(); to.After(now) {
		to = now
		if to.Before(from) {
			to = from
		}
	}
	if !to.After(from) {
		return Series{From: from, To: to, Window: window, Samples: emptyRuns(providers)}, nil
	}

	n := int((to.Sub(from) + window - 1) / window)
	if n > MaxSamples {
		return Series{}, errTooManySamples(n)
	}

	runs := make(map[did.DID][]Sample, len(providers))
	for _, provider := range providers {
		samples, err := s.run(ctx, provider, space, from, to, window, n)
		if err != nil {
			return Series{}, err
		}
		runs[provider] = samples
	}

	return Series{From: from, To: to, Window: window, Samples: runs}, nil
}

// emptyRuns gives every provider an empty run, so a caller can index the map by
// provider whether or not the range covered anything.
func emptyRuns(providers []did.DID) map[did.DID][]Sample {
	runs := make(map[did.DID][]Sample, len(providers))
	for _, p := range providers {
		runs[p] = []Sample{}
	}
	return runs
}

// run replays one provider's diffs into n buckets of window over [from, to).
func (s *Service) run(ctx context.Context, provider, space did.DID, from, to time.Time, window time.Duration, n int) ([]Sample, error) {
	stored, rows, err := s.read(ctx, provider, space, from)
	if err != nil {
		return nil, err
	}

	// Bucket k covers [from+(k-1)*window, from+k*window) and is indexed from 1.
	deltas := make([]int64, n+1)
	ingested := make([]uint64, n+1)

	// Changes at or after `to` are what separates the counters, which describe
	// now, from the stored bytes at `to`.
	var tail int64

	for _, r := range rows {
		switch {
		case r.ReceiptAt.Before(from):
			// Earlier than the range, so already part of the stored bytes at
			// every bucket within it. Unwinding it would subtract it twice.
			continue
		case !r.ReceiptAt.Before(to):
			tail += r.Delta
		default:
			k := int(r.ReceiptAt.Sub(from)/window) + 1
			if k > n {
				k = n
			}
			deltas[k] += r.Delta
			if r.Delta > 0 {
				ingested[k] += uint64(r.Delta)
			}
		}
	}

	// Walk back from the stored bytes at `to`, shedding each bucket's changes to
	// reach the bytes held when that bucket opened.
	samples := make([]Sample, n)
	cur := stored - tail
	for k := n; k >= 1; k-- {
		end := from.Add(time.Duration(k) * window)
		if end.After(to) {
			end = to
		}
		held := cur
		if held < 0 {
			// The counters and the diff log disagree. Report nothing held
			// rather than a wrapped value, and keep going: the rest of the
			// series is still worth more than a failed call.
			s.logger.Warn("space usage unwound below zero",
				zap.Stringer("space", space),
				zap.Time("bucket", end),
				zap.Int64("bytes", held))
			held = 0
		}
		samples[k-1] = Sample{End: end, BytesStored: uint64(held), BytesIngested: ingested[k]}
		cur -= deltas[k]
	}

	return samples, nil
}

// read returns the bytes the space currently holds according to one provider,
// together with that provider's diffs from the start of the range onwards, both
// describing the same instant.
//
// The two come from different tables and one total anchors every sample, so a
// blob landing between the reads would shift the whole series by its size. The
// counters only ever increase, so reading them again after the scan detects
// exactly that: unchanged means nothing committed for this space while the scan
// ran. A change and an equal removal are caught too, because the counters are
// compared separately rather than by their difference.
func (s *Service) read(ctx context.Context, provider, space did.DID, from time.Time) (int64, []spacediff.DifferenceRecord, error) {
	totals, err := s.spaceMetrics.Get(ctx, provider, space)
	if err != nil {
		return 0, nil, fmt.Errorf("getting space metrics: %w", err)
	}

	for attempt := 1; ; attempt++ {
		rows, err := s.scan(ctx, provider, space, from)
		if err != nil {
			return 0, nil, err
		}

		after, err := s.spaceMetrics.Get(ctx, provider, space)
		if err != nil {
			return 0, nil, fmt.Errorf("getting space metrics: %w", err)
		}

		if after[metrics.BlobAddSizeTotalMetric] == totals[metrics.BlobAddSizeTotalMetric] &&
			after[metrics.BlobRemoveSizeTotalMetric] == totals[metrics.BlobRemoveSizeTotalMetric] {
			stored, err := storedBytes(totals)
			if err != nil {
				return 0, nil, err
			}
			return stored, rows, nil
		}

		if attempt >= totalsAttempts {
			s.logger.Warn("gave up reading consistent space usage",
				zap.Stringer("space", space), zap.Int("attempts", attempt))
			return 0, nil, ErrUsageUnstable
		}
		totals = after
	}
}

// scan collects the diffs recorded from the start of the range onwards. The
// store's lower bound is exclusive and a change recorded exactly at the start
// belongs to the first bucket, so the bound is backed off by a second and the
// earlier rows are dropped by the caller.
//
// This pages by hand rather than through store.Collect so the row count can be
// bounded.
func (s *Service) scan(ctx context.Context, provider, space did.DID, from time.Time) ([]spacediff.DifferenceRecord, error) {
	after := from.Add(-time.Second)

	var rows []spacediff.DifferenceRecord
	var cursor *string
	for {
		var opts []spacediff.ListOption
		if cursor != nil {
			opts = append(opts, spacediff.WithListCursor(*cursor))
		}
		page, err := s.diffs.List(ctx, provider, space, after, opts...)
		if err != nil {
			return nil, fmt.Errorf("listing space diffs: %w", err)
		}
		rows = append(rows, page.Results...)
		if len(rows) > maxScanRows {
			return nil, ErrRangeTooBusy
		}
		if page.Cursor == nil || len(page.Results) == 0 {
			return rows, nil
		}
		cursor = page.Cursor
	}
}

// storedBytes derives the bytes a space currently holds from its add and remove
// counters, which only ever increase.
func storedBytes(totals map[string]uint64) (int64, error) {
	added := totals[metrics.BlobAddSizeTotalMetric]
	removed := totals[metrics.BlobRemoveSizeTotalMetric]
	if removed > added || added-removed > math.MaxInt64 {
		return 0, fmt.Errorf("space byte counters inconsistent: added %d, removed %d", added, removed)
	}
	return int64(added - removed), nil
}
