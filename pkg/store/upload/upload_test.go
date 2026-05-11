package upload_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/upload"
	"github.com/fil-forge/sprue/pkg/store/upload/aws"
	"github.com/fil-forge/sprue/pkg/store/upload/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/google/uuid"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
)

type StoreKind string

const (
	Memory   StoreKind = "memory"
	AWS      StoreKind = "aws"
	Postgres StoreKind = "postgres"
)

var storeKinds = []StoreKind{Memory, AWS, Postgres}

func makeStore(t *testing.T, k StoreKind) upload.Store {
	switch k {
	case Memory:
		return memory.New()
	case AWS:
		return createAWSStore(t)
	case Postgres:
		return createPostgresStore(t)
	}
	panic("unknown store kind")
}

func createPostgresStore(t *testing.T) upload.Store {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	return uploadpostgres.New(pool)
}

func createAWSStore(t *testing.T) *aws.Store {
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

	s3Endpoint := testutil.CreateS3(t)
	s3 := testutil.NewS3Client(t, s3Endpoint)

	dynamoEndpoint := testutil.CreateDynamo(t)
	dynamo := testutil.NewDynamoClient(t, dynamoEndpoint)

	id := uuid.NewString()
	store := aws.New(dynamo, "upload-"+id, s3, "upload-shards-"+id)

	err := store.Initialize(t.Context())
	require.NoError(t, err)
	return store
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
			store := makeStore(t, k)
			t.Run("adds an upload", func(t *testing.T) {
				space := testutil.RandomDID(t)
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
				// first batch and enough new shards to push the total over ShardThreshold
				newShardCount := aws.ShardThreshold - len(initialShards) + 2 // +2 to exceed threshold, accounting for the duplicate
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
				require.Greater(t, wantShards, aws.ShardThreshold)

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
					{"many shards", aws.ShardThreshold + 1},
				}
				for _, tc := range cases {
					t.Run(tc.name, func(t *testing.T) {
						space := testutil.RandomDID(t)
						root := testutil.RandomCID(t)
						index := testutil.RandomCID(t)
						cause := testutil.RandomCID(t)

						// removing a non-existent upload returns an error
						err := store.Remove(t.Context(), space, root)
						require.ErrorIs(t, err, upload.ErrUploadNotFound)

						shards := make([]cid.Cid, tc.shardCount)
						for i := range shards {
							shards[i] = testutil.RandomCID(t)
						}

						err = store.Upsert(t.Context(), space, root, &index, shards, cause)
						require.NoError(t, err)

						err = store.Remove(t.Context(), space, root)
						require.NoError(t, err)

						exists, err := store.Exists(t.Context(), space, root)
						require.NoError(t, err)
						require.False(t, exists)

						// shards should also be gone
						allShards := listAllShards(t, store, space, root)
						require.Empty(t, allShards)

						// removing again returns an error
						err = store.Remove(t.Context(), space, root)
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
					{"many shards", aws.ShardThreshold + 1},
				}
				for _, tc := range cases {
					t.Run(tc.name, func(t *testing.T) {
						space := testutil.RandomDID(t)
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
		})
	}
}
