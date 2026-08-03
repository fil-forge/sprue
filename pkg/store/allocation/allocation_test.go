package allocation_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/allocation"
	allocationmemory "github.com/fil-forge/sprue/pkg/store/allocation/memory"
	allocationpostgres "github.com/fil-forge/sprue/pkg/store/allocation/postgres"
	"github.com/stretchr/testify/require"
)

type StoreKind string

const (
	Memory   StoreKind = "memory"
	Postgres StoreKind = "postgres"
)

var storeKinds = []StoreKind{Memory, Postgres}

func makeStore(t *testing.T, k StoreKind) allocation.Store {
	switch k {
	case Memory:
		return allocationmemory.New()
	case Postgres:
		return createPostgresStore(t)
	}
	panic("unknown store kind")
}

func createPostgresStore(t *testing.T) allocation.Store {
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
	return allocationpostgres.New(testutil.CreatePostgres(t))
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
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				bl := randomBlob(t, 1024)
				require.NoError(t, s.Add(t.Context(), space, bl, cause))

				rec, err := s.Get(t.Context(), space, bl.Digest)
				require.NoError(t, err)
				require.Equal(t, space, rec.Space)
				require.Equal(t, bl.Digest, rec.Blob.Digest)
				require.Equal(t, bl.Size, rec.Blob.Size)
				require.Equal(t, cause, rec.Cause)
				require.False(t, rec.InsertedAt.IsZero())
			})

			t.Run("returns ErrEntryExists when adding a duplicate", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				bl := randomBlob(t, 512)
				require.NoError(t, s.Add(t.Context(), space, bl, cause))

				err := s.Add(t.Context(), space, bl, cause)
				require.ErrorIs(t, err, allocation.ErrEntryExists)
			})

			t.Run("Get returns ErrEntryNotFound for unknown allocation", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)

				_, err := s.Get(t.Context(), space, testutil.RandomMultihash(t))
				require.ErrorIs(t, err, allocation.ErrEntryNotFound)
			})

			t.Run("Get isolates allocations between spaces", func(t *testing.T) {
				s := makeStore(t, k)
				space1 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				bl := randomBlob(t, 1024)
				require.NoError(t, s.Add(t.Context(), space1, bl, cause))

				_, err := s.Get(t.Context(), space2, bl.Digest)
				require.ErrorIs(t, err, allocation.ErrEntryNotFound)
			})

			t.Run("removes an allocation", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				bl := randomBlob(t, 2048)
				require.NoError(t, s.Add(t.Context(), space, bl, cause))

				require.NoError(t, s.Remove(t.Context(), space, bl.Digest, testutil.RandomCID(t)))

				_, err := s.Get(t.Context(), space, bl.Digest)
				require.ErrorIs(t, err, allocation.ErrEntryNotFound)
			})

			t.Run("Remove returns ErrEntryNotFound for unknown allocation", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)

				err := s.Remove(t.Context(), space, testutil.RandomMultihash(t), testutil.RandomCID(t))
				require.ErrorIs(t, err, allocation.ErrEntryNotFound)
			})

			t.Run("adds an allocation again after removal", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				bl := randomBlob(t, 512)
				require.NoError(t, s.Add(t.Context(), space, bl, cause))
				require.NoError(t, s.Remove(t.Context(), space, bl.Digest, testutil.RandomCID(t)))
				require.NoError(t, s.Add(t.Context(), space, bl, cause))

				rec, err := s.Get(t.Context(), space, bl.Digest)
				require.NoError(t, err)
				require.Equal(t, bl.Digest, rec.Blob.Digest)
			})

			t.Run("lists allocations for a space", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				for range 3 {
					require.NoError(t, s.Add(t.Context(), space, randomBlob(t, 512), cause))
				}

				page, err := s.List(t.Context(), space)
				require.NoError(t, err)
				require.Len(t, page.Results, 3)
			})

			t.Run("List returns empty page for unknown space", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)

				page, err := s.List(t.Context(), space)
				require.NoError(t, err)
				require.Empty(t, page.Results)
				require.Nil(t, page.Cursor)
			})

			t.Run("List paginates results", func(t *testing.T) {
				s := makeStore(t, k)
				space := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				for range 5 {
					require.NoError(t, s.Add(t.Context(), space, randomBlob(t, 512), cause))
				}

				all, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[allocation.Record], error) {
					listOpts := []allocation.ListOption{allocation.WithListLimit(2)}
					if opts.Cursor != nil {
						listOpts = append(listOpts, allocation.WithListCursor(*opts.Cursor))
					}
					return s.List(ctx, space, listOpts...)
				})
				require.NoError(t, err)
				require.Len(t, all, 5)
			})

			t.Run("List isolates allocations between spaces", func(t *testing.T) {
				s := makeStore(t, k)
				space1 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				require.NoError(t, s.Add(t.Context(), space1, randomBlob(t, 512), cause))
				require.NoError(t, s.Add(t.Context(), space1, randomBlob(t, 512), cause))
				require.NoError(t, s.Add(t.Context(), space2, randomBlob(t, 512), cause))

				page1, err := s.List(t.Context(), space1)
				require.NoError(t, err)
				require.Len(t, page1.Results, 2)

				page2, err := s.List(t.Context(), space2)
				require.NoError(t, err)
				require.Len(t, page2.Results, 1)
			})
		})
	}
}
