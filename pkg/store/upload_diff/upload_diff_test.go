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
				provider := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				receiptAt := time.Now().UTC().Truncate(time.Millisecond)

				err := s.Put(t.Context(), provider, space, "sub1", cause, 1, receiptAt)
				require.NoError(t, err)

				page, err := s.List(t.Context(), provider, space, time.Time{})
				require.NoError(t, err)
				require.Len(t, page.Results, 1)

				rec := page.Results[0]
				require.Equal(t, provider, rec.Provider)
				require.Equal(t, space, rec.Space)
				require.Equal(t, "sub1", rec.Subscription)
				require.Equal(t, cause, rec.Cause)
				require.Equal(t, int64(1), rec.Delta)
				require.WithinDuration(t, receiptAt, rec.ReceiptAt, time.Second)
			})

			t.Run("lists diffs after a given time", func(t *testing.T) {
				s := makeStore(t, k)
				provider := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				base := time.Now().UTC()
				t1 := base.Add(-3 * time.Hour)
				t2 := base.Add(-2 * time.Hour)
				t3 := base.Add(-1 * time.Hour)

				require.NoError(t, s.Put(t.Context(), provider, space, "sub1", cause, 100, t1))
				require.NoError(t, s.Put(t.Context(), provider, space, "sub1", cause, 200, t2))
				require.NoError(t, s.Put(t.Context(), provider, space, "sub1", cause, 300, t3))

				// after epoch zero returns all
				page, err := s.List(t.Context(), provider, space, time.Time{})
				require.NoError(t, err)
				require.Len(t, page.Results, 3)

				// after t1 excludes the first entry
				page, err = s.List(t.Context(), provider, space, t1)
				require.NoError(t, err)
				require.Len(t, page.Results, 2)
				require.Equal(t, int64(200), page.Results[0].Delta)
				require.Equal(t, int64(300), page.Results[1].Delta)

				// after t3 returns nothing
				page, err = s.List(t.Context(), provider, space, t3)
				require.NoError(t, err)
				require.Empty(t, page.Results)
			})

			t.Run("returns empty list for unknown provider/space", func(t *testing.T) {
				s := makeStore(t, k)
				provider := testutil.RandomDID(t)
				space := testutil.RandomDID(t)

				page, err := s.List(t.Context(), provider, space, time.Time{})
				require.NoError(t, err)
				require.Empty(t, page.Results)
				require.Nil(t, page.Cursor)
			})

			t.Run("results are ordered by receiptAt ascending", func(t *testing.T) {
				s := makeStore(t, k)
				provider := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				base := time.Now().UTC()
				// insert out of order
				require.NoError(t, s.Put(t.Context(), provider, space, "sub1", cause, 300, base.Add(-1*time.Hour)))
				require.NoError(t, s.Put(t.Context(), provider, space, "sub1", cause, 100, base.Add(-3*time.Hour)))
				require.NoError(t, s.Put(t.Context(), provider, space, "sub1", cause, 200, base.Add(-2*time.Hour)))

				page, err := s.List(t.Context(), provider, space, time.Time{})
				require.NoError(t, err)
				require.Len(t, page.Results, 3)
				require.Equal(t, int64(100), page.Results[0].Delta)
				require.Equal(t, int64(200), page.Results[1].Delta)
				require.Equal(t, int64(300), page.Results[2].Delta)
			})

			t.Run("paginates results", func(t *testing.T) {
				s := makeStore(t, k)
				provider := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				base := time.Now().UTC()
				for i := range 5 {
					require.NoError(t, s.Put(t.Context(), provider, space, "sub1", cause, int64(i+1)*100, base.Add(time.Duration(i)*time.Hour)))
				}

				// first page of 2
				page, err := s.List(t.Context(), provider, space, time.Time{}, uploaddiff.WithListLimit(2))
				require.NoError(t, err)
				require.Len(t, page.Results, 2)
				require.NotNil(t, page.Cursor)

				// second page using cursor
				page, err = s.List(t.Context(), provider, space, time.Time{}, uploaddiff.WithListCursor(*page.Cursor))
				require.NoError(t, err)
				require.Len(t, page.Results, 3)
			})

			t.Run("collects all diffs via pagination", func(t *testing.T) {
				s := makeStore(t, k)
				provider := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				after := time.Time{}

				base := time.Now().UTC()
				for i := range 5 {
					require.NoError(t, s.Put(t.Context(), provider, space, "sub1", cause, int64(i+1)*100, base.Add(time.Duration(i)*time.Hour)))
				}

				all, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[uploaddiff.DifferenceRecord], error) {
					listOpts := []uploaddiff.ListOption{uploaddiff.WithListLimit(2)}
					if opts.Cursor != nil {
						listOpts = append(listOpts, uploaddiff.WithListCursor(*opts.Cursor))
					}
					return s.List(ctx, provider, space, after, listOpts...)
				})
				require.NoError(t, err)
				require.Len(t, all, 5)
			})

			t.Run("isolates diffs between spaces", func(t *testing.T) {
				s := makeStore(t, k)
				provider := testutil.RandomDID(t)
				space1 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				base := time.Now().UTC()
				require.NoError(t, s.Put(t.Context(), provider, space1, "sub1", cause, 100, base))
				require.NoError(t, s.Put(t.Context(), provider, space2, "sub1", cause, 200, base))

				page1, err := s.List(t.Context(), provider, space1, time.Time{})
				require.NoError(t, err)
				require.Len(t, page1.Results, 1)
				require.Equal(t, int64(100), page1.Results[0].Delta)

				page2, err := s.List(t.Context(), provider, space2, time.Time{})
				require.NoError(t, err)
				require.Len(t, page2.Results, 1)
				require.Equal(t, int64(200), page2.Results[0].Delta)
			})

			t.Run("isolates diffs between providers", func(t *testing.T) {
				s := makeStore(t, k)
				provider1 := testutil.RandomDID(t)
				provider2 := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				base := time.Now().UTC()
				require.NoError(t, s.Put(t.Context(), provider1, space, "sub1", cause, 100, base))
				require.NoError(t, s.Put(t.Context(), provider2, space, "sub1", cause, 200, base))

				page1, err := s.List(t.Context(), provider1, space, time.Time{})
				require.NoError(t, err)
				require.Len(t, page1.Results, 1)
				require.Equal(t, int64(100), page1.Results[0].Delta)

				page2, err := s.List(t.Context(), provider2, space, time.Time{})
				require.NoError(t, err)
				require.Len(t, page2.Results, 1)
				require.Equal(t, int64(200), page2.Results[0].Delta)
			})

			t.Run("supports negative deltas", func(t *testing.T) {
				s := makeStore(t, k)
				provider := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				err := s.Put(t.Context(), provider, space, "sub1", cause, -1, time.Now().UTC())
				require.NoError(t, err)

				page, err := s.List(t.Context(), provider, space, time.Time{})
				require.NoError(t, err)
				require.Len(t, page.Results, 1)
				require.Equal(t, int64(-1), page.Results[0].Delta)
			})

			t.Run("a non-positive limit falls back to the default", func(t *testing.T) {
				s := makeStore(t, k)
				provider := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				base := time.Now().UTC().Truncate(time.Millisecond)
				for i := range 3 {
					require.NoError(t, s.Put(t.Context(), provider, space, "sub1",
						testutil.RandomCID(t), 1, base.Add(time.Duration(i)*time.Millisecond)))
				}

				// Zero means "unset", as it does in Postgres. Slicing a page to
				// [:0] and then reading its last element would panic.
				page, err := s.List(t.Context(), provider, space, time.Time{}, uploaddiff.WithListLimit(0))
				require.NoError(t, err)
				require.Len(t, page.Results, 3)
				require.Nil(t, page.Cursor)
			})

			t.Run("paginates through diffs sharing a timestamp", func(t *testing.T) {
				s := makeStore(t, k)
				provider := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				// One recorded change writes a row per consumer at the same
				// instant, so a page boundary landing inside such a group is
				// ordinary. A cursor carrying only the timestamp would skip the
				// rest of the group.
				at := time.Now().UTC().Truncate(time.Millisecond)
				const total = 5
				for range total {
					require.NoError(t, s.Put(t.Context(), provider, space, "sub1", testutil.RandomCID(t), 1, at))
				}

				seen := map[string]bool{}
				var cursor *string
				for {
					opts := []uploaddiff.ListOption{uploaddiff.WithListLimit(2)}
					if cursor != nil {
						opts = append(opts, uploaddiff.WithListCursor(*cursor))
					}
					page, err := s.List(t.Context(), provider, space, time.Time{}, opts...)
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
				// Regression guard: reads must not create the provider/space
				// entries, which under a read lock would race.
				s := makeStore(t, k)
				provider := testutil.RandomDID(t)

				var wg sync.WaitGroup
				for range 8 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, err := s.List(context.Background(), provider, testutil.RandomDID(t), time.Time{})
						assert.NoError(t, err)
					}()
				}
				wg.Wait()
			})
		})
	}
}
