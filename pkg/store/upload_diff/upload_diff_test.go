package uploaddiff_test

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store"
	uploaddiff "github.com/fil-forge/sprue/pkg/store/upload_diff"
	uploaddiffmemory "github.com/fil-forge/sprue/pkg/store/upload_diff/memory"
	uploaddiffpostgres "github.com/fil-forge/sprue/pkg/store/upload_diff/postgres"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type StoreKind string

const (
	Memory   StoreKind = "memory"
	Postgres StoreKind = "postgres"
)

var storeKinds = []StoreKind{Memory, Postgres}

func makeStore(t *testing.T, k StoreKind) uploaddiff.Store {
	switch k {
	case Memory:
		return uploaddiffmemory.New()
	case Postgres:
		return createPostgresStore(t)
	}
	panic("unknown store kind")
}

func createPostgresStore(t *testing.T) *uploaddiffpostgres.Store {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	return uploaddiffpostgres.New(pool)
}

func TestUploadDiffStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			t.Run("puts a diff", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				receiptAt := time.Now().UTC().Truncate(time.Millisecond)

				mustPut(t, s, t.Context(), space, cause, 1, receiptAt)

				page, err := s.List(t.Context(), space, time.Time{})
				require.NoError(t, err)
				require.Len(t, page.Results, 1)

				rec := page.Results[0]
				require.Equal(t, space, rec.Space)
				require.Equal(t, cause, rec.Cause)
				require.Equal(t, int64(1), rec.Delta)
				require.WithinDuration(t, receiptAt, rec.ReceiptAt, time.Second)
			})

			t.Run("lists diffs after a given time", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				base := time.Now().UTC()
				t1 := base.Add(-3 * time.Hour)
				t2 := base.Add(-2 * time.Hour)
				t3 := base.Add(-1 * time.Hour)

				mustPut(t, s, t.Context(), space, cause, 100, t1)
				mustPut(t, s, t.Context(), space, cause, 200, t2)
				mustPut(t, s, t.Context(), space, cause, 300, t3)

				// after epoch zero returns all
				page, err := s.List(t.Context(), space, time.Time{})
				require.NoError(t, err)
				require.Len(t, page.Results, 3)

				// after t1 excludes the first entry
				page, err = s.List(t.Context(), space, t1)
				require.NoError(t, err)
				require.Len(t, page.Results, 2)
				require.Equal(t, int64(200), page.Results[0].Delta)
				require.Equal(t, int64(300), page.Results[1].Delta)

				// after t3 returns nothing
				page, err = s.List(t.Context(), space, t3)
				require.NoError(t, err)
				require.Empty(t, page.Results)
			})

			t.Run("returns empty list for an unknown space", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)

				page, err := s.List(t.Context(), space, time.Time{})
				require.NoError(t, err)
				require.Empty(t, page.Results)
				require.Nil(t, page.Cursor)
			})

			t.Run("results are ordered by receiptAt ascending", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				base := time.Now().UTC()
				// insert out of order
				mustPut(t, s, t.Context(), space, cause, 300, base.Add(-1*time.Hour))
				mustPut(t, s, t.Context(), space, cause, 100, base.Add(-3*time.Hour))
				mustPut(t, s, t.Context(), space, cause, 200, base.Add(-2*time.Hour))

				page, err := s.List(t.Context(), space, time.Time{})
				require.NoError(t, err)
				require.Len(t, page.Results, 3)
				require.Equal(t, int64(100), page.Results[0].Delta)
				require.Equal(t, int64(200), page.Results[1].Delta)
				require.Equal(t, int64(300), page.Results[2].Delta)
			})

			t.Run("paginates results", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				base := time.Now().UTC()
				for i := range 5 {
					mustPut(t, s, t.Context(), space, cause, int64(i+1)*100, base.Add(time.Duration(i)*time.Hour))
				}

				// first page of 2
				page, err := s.List(t.Context(), space, time.Time{}, uploaddiff.WithListLimit(2))
				require.NoError(t, err)
				require.Len(t, page.Results, 2)
				require.NotNil(t, page.Cursor)

				// second page using cursor
				page, err = s.List(t.Context(), space, time.Time{}, uploaddiff.WithListCursor(*page.Cursor))
				require.NoError(t, err)
				require.Len(t, page.Results, 3)
			})

			t.Run("collects all diffs via pagination", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				after := time.Time{}

				base := time.Now().UTC()
				for i := range 5 {
					mustPut(t, s, t.Context(), space, cause, int64(i+1)*100, base.Add(time.Duration(i)*time.Hour))
				}

				all, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[uploaddiff.DifferenceRecord], error) {
					listOpts := []uploaddiff.ListOption{uploaddiff.WithListLimit(2)}
					if opts.Cursor != nil {
						listOpts = append(listOpts, uploaddiff.WithListCursor(*opts.Cursor))
					}
					return s.List(ctx, space, after, listOpts...)
				})
				require.NoError(t, err)
				require.Len(t, all, 5)
			})

			t.Run("isolates diffs between spaces", func(t *testing.T) {
				s := makeStore(t, k)
				space1 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				base := time.Now().UTC()
				mustPut(t, s, t.Context(), space1, cause, 100, base)
				mustPut(t, s, t.Context(), space2, cause, 200, base)

				page1, err := s.List(t.Context(), space1, time.Time{})
				require.NoError(t, err)
				require.Len(t, page1.Results, 1)
				require.Equal(t, int64(100), page1.Results[0].Delta)

				page2, err := s.List(t.Context(), space2, time.Time{})
				require.NoError(t, err)
				require.Len(t, page2.Results, 1)
				require.Equal(t, int64(200), page2.Results[0].Delta)
			})

			t.Run("supports negative deltas", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				mustPut(t, s, t.Context(), space, cause, -1, time.Now().UTC())

				page, err := s.List(t.Context(), space, time.Time{})
				require.NoError(t, err)
				require.Len(t, page.Results, 1)
				require.Equal(t, int64(-1), page.Results[0].Delta)
			})

			t.Run("a non-positive limit falls back to the default", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				base := time.Now().UTC().Truncate(time.Millisecond)
				for i := range 3 {
					mustPut(t, s, t.Context(), space,
						testutil.RandomCID(t), 1, base.Add(time.Duration(i)*time.Millisecond))
				}

				// Zero means "unset", as it does in Postgres. Slicing a page to
				// [:0] and then reading its last element would panic.
				page, err := s.List(t.Context(), space, time.Time{}, uploaddiff.WithListLimit(0))
				require.NoError(t, err)
				require.Len(t, page.Results, 3)
				require.Nil(t, page.Cursor)
			})

			t.Run("paginates through diffs sharing a timestamp", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				// One recorded change writes a row per consumer at the same
				// instant, so a page boundary landing inside such a group is
				// ordinary. A cursor carrying only the timestamp would skip the
				// rest of the group.
				at := time.Now().UTC().Truncate(time.Millisecond)
				const total = 5
				for range total {
					mustPut(t, s, t.Context(), space, testutil.RandomCID(t), 1, at)
				}

				seen := map[string]bool{}
				var cursor *string
				for {
					opts := []uploaddiff.ListOption{uploaddiff.WithListLimit(2)}
					if cursor != nil {
						opts = append(opts, uploaddiff.WithListCursor(*cursor))
					}
					page, err := s.List(t.Context(), space, time.Time{}, opts...)
					require.NoError(t, err)
					for _, r := range page.Results {
						require.False(t, seen[r.Cause.String()], "row listed twice")
						seen[r.Cause.String()] = true
					}
					if page.Cursor == nil {
						break
					}
					cursor = page.Cursor
				}
				require.Len(t, seen, total, "every row must be listed exactly once")
			})

			t.Run("concurrent reads of an unwritten space are safe", func(t *testing.T) {
				// Regression guard: reads must not create the space entry,
				// which under a read lock would race.
				s := makeStore(t, k)

				var wg sync.WaitGroup
				for range 8 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, err := s.List(context.Background(), testutil.RandomDID(t), time.Time{})
						assert.NoError(t, err)
					}()
				}
				wg.Wait()
			})
		})
	}
}

// mustPut records a change and asserts the store accepted it as new.
func mustPut(t *testing.T, s uploaddiff.Store, ctx context.Context, space did.DID, cause cid.Cid, delta int64, receiptAt time.Time) {
	t.Helper()
	recorded, err := s.Put(ctx, space, cause, delta, receiptAt)
	require.NoError(t, err)
	require.True(t, recorded, "the change should be new to the log")
}

// TestPutReportsARepeatedChange: the same (space, receipt_at, cause) is one
// change recorded twice. Both backends suppress it and say so, because the
// caller gates its running total on the answer — a counter that moved while
// the log did not would leave a reconstructed count permanently offset.
func TestPutReportsARepeatedChange(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			s := makeStore(t, k)
			space := testutil.RandomDID(t)
			cause := testutil.RandomCID(t)
			at := time.Now().UTC().Truncate(time.Millisecond)

			recorded, err := s.Put(t.Context(), space, cause, 1, at)
			require.NoError(t, err)
			require.True(t, recorded)

			recorded, err = s.Put(t.Context(), space, cause, 1, at)
			require.NoError(t, err)
			require.False(t, recorded, "a repeat of the same change is not recorded again")

			page, err := s.List(t.Context(), space, time.Time{})
			require.NoError(t, err)
			require.Len(t, page.Results, 1, "and leaves one row, not two")
		})
	}
}

// TestChangesWithinAMillisecondStayDistinct: two changes less than a
// millisecond apart are two changes, in both backends. The log keeps times to
// the microsecond, so nothing coarser may decide whether a change is new —
// the caller gates its running total on that answer, and a backend that
// collapsed the pair would drop a real change on one deployment and not the
// other.
func TestChangesWithinAMillisecondStayDistinct(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			s := makeStore(t, k)
			space := testutil.RandomDID(t)
			cause := testutil.RandomCID(t)
			first := time.Now().UTC().Truncate(time.Microsecond)
			second := first.Add(100 * time.Microsecond)

			recorded, err := s.Put(t.Context(), space, cause, 1, first)
			require.NoError(t, err)
			require.True(t, recorded)

			recorded, err = s.Put(t.Context(), space, cause, 1, second)
			require.NoError(t, err)
			require.True(t, recorded, "100µs later is a different instant, not a repeat")

			page, err := s.List(t.Context(), space, time.Time{})
			require.NoError(t, err)
			require.Len(t, page.Results, 2)
			// Stored at the precision the caller gave, so a cursor built from
			// one row addresses exactly that row in either backend.
			require.Equal(t, first, page.Results[0].ReceiptAt.UTC())
			require.Equal(t, second, page.Results[1].ReceiptAt.UTC())
		})
	}
}
