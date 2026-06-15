package storageprovider_test

import (
	"context"
	"net/url"
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	storageprovideraws "github.com/fil-forge/sprue/pkg/store/storage_provider/aws"
	storageprovidermemory "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	storageproviderpostgres "github.com/fil-forge/sprue/pkg/store/storage_provider/postgres"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/command"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/delegation"
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

func makeStore(t *testing.T, k StoreKind) storageprovider.Store {
	switch k {
	case Memory:
		return storageprovidermemory.New()
	case AWS:
		return createAWSStore(t)
	case Postgres:
		return createPostgresStore(t)
	}
	panic("unknown store kind")
}

func createPostgresStore(t *testing.T) storageprovider.Store {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	return storageproviderpostgres.New(pool)
}

func createAWSStore(t *testing.T) storageprovider.Store {
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

	s := storageprovideraws.New(dynamo, "storage-provider-"+uuid.NewString())
	require.NoError(t, s.Initialize(t.Context()))
	return s
}

// randomEndpoint returns a random HTTPS endpoint URL.
func randomEndpoint(t *testing.T) url.URL {
	t.Helper()
	u, err := url.Parse("https://" + uuid.NewString() + ".example.com")
	require.NoError(t, err)
	return *u
}

// randomProofs returns a container holding a single delegation, used to
// exercise proof serialization round-trips.
func randomProofs(t *testing.T) ucan.Container {
	t.Helper()
	dlg, err := delegation.Delegate(testutil.Alice, testutil.Bob.DID(), testutil.Alice.DID(), command.MustParse("/blob/allocate"))
	require.NoError(t, err)
	return container.New(container.WithDelegations(dlg))
}

func TestStorageProviderStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			s := makeStore(t, k)

			t.Run("puts and gets a provider", func(t *testing.T) {
				provider := testutil.Alice
				endpoint := randomEndpoint(t)
				weight := 10
				replWeight := 5
				proofs := randomProofs(t)

				require.NoError(t, s.Put(t.Context(), provider.DID(), endpoint, weight, &replWeight, proofs))

				rec, err := s.Get(t.Context(), provider.DID())
				require.NoError(t, err)
				require.Equal(t, provider.DID(), rec.Provider)
				require.Equal(t, endpoint, rec.Endpoint)
				require.Equal(t, weight, rec.Weight)
				require.Equal(t, replWeight, *rec.ReplicationWeight)
				require.False(t, rec.InsertedAt.IsZero())

				require.NotNil(t, rec.Proofs)
				wantBytes, err := container.Encode(container.Raw, proofs)
				require.NoError(t, err)
				gotBytes, err := container.Encode(container.Raw, rec.Proofs)
				require.NoError(t, err)
				require.Equal(t, wantBytes, gotBytes)
			})

			t.Run("Put returns an error when proofs are missing", func(t *testing.T) {
				provider := testutil.Alice
				endpoint := randomEndpoint(t)

				require.Error(t, s.Put(t.Context(), provider.DID(), endpoint, 10, nil, nil))
			})

			t.Run("put updates an existing provider", func(t *testing.T) {
				provider := testutil.Alice
				endpoint1 := randomEndpoint(t)
				endpoint2 := randomEndpoint(t)
				weight1 := 10
				weight2 := 20
				replWeight1 := 5
				replWeight2 := 15

				require.NoError(t, s.Put(t.Context(), provider.DID(), endpoint1, weight1, &replWeight1, randomProofs(t)))
				require.NoError(t, s.Put(t.Context(), provider.DID(), endpoint2, weight2, &replWeight2, randomProofs(t)))

				rec, err := s.Get(t.Context(), provider.DID())
				require.NoError(t, err)
				require.Equal(t, endpoint2, rec.Endpoint)
				require.Equal(t, weight2, rec.Weight)
				require.Equal(t, replWeight2, *rec.ReplicationWeight)
			})

			t.Run("Put with nil replicationWeight clears a previously-stored value", func(t *testing.T) {
				provider := testutil.Alice
				endpoint := randomEndpoint(t)
				weight := 10
				replWeight := 5

				require.NoError(t, s.Put(t.Context(), provider.DID(), endpoint, weight, &replWeight, randomProofs(t)))

				rec, err := s.Get(t.Context(), provider.DID())
				require.NoError(t, err)
				require.NotNil(t, rec.ReplicationWeight)

				require.NoError(t, s.Put(t.Context(), provider.DID(), endpoint, weight, nil, randomProofs(t)))

				rec, err = s.Get(t.Context(), provider.DID())
				require.NoError(t, err)
				require.Nil(t, rec.ReplicationWeight)
			})

			t.Run("Put accepts a nil replicationWeight on insert", func(t *testing.T) {
				provider := testutil.Bob
				endpoint := randomEndpoint(t)

				require.NoError(t, s.Put(t.Context(), provider.DID(), endpoint, 10, nil, randomProofs(t)))

				rec, err := s.Get(t.Context(), provider.DID())
				require.NoError(t, err)
				require.Nil(t, rec.ReplicationWeight)
			})

			t.Run("Get returns ErrStorageProviderNotFound for unknown provider", func(t *testing.T) {
				provider := testutil.RandomDID(t)

				_, err := s.Get(t.Context(), provider)
				require.ErrorIs(t, err, storageprovider.ErrStorageProviderNotFound)
			})

			t.Run("deletes a provider", func(t *testing.T) {
				provider := testutil.Alice
				endpoint := randomEndpoint(t)
				weight := 10
				replWeight := 5

				require.NoError(t, s.Put(t.Context(), provider.DID(), endpoint, weight, &replWeight, randomProofs(t)))
				require.NoError(t, s.Delete(t.Context(), provider.DID()))

				_, err := s.Get(t.Context(), provider.DID())
				require.ErrorIs(t, err, storageprovider.ErrStorageProviderNotFound)
			})

			t.Run("Delete returns ErrStorageProviderNotFound for unknown provider", func(t *testing.T) {
				provider := testutil.RandomDID(t)

				err := s.Delete(t.Context(), provider)
				require.ErrorIs(t, err, storageprovider.ErrStorageProviderNotFound)
			})

			t.Run("List includes added providers", func(t *testing.T) {
				provider1 := testutil.Alice
				provider2 := testutil.Bob
				endpoint := randomEndpoint(t)
				weight := 10
				replWeight := 5

				require.NoError(t, s.Put(t.Context(), provider1.DID(), endpoint, weight, &replWeight, randomProofs(t)))
				require.NoError(t, s.Put(t.Context(), provider2.DID(), endpoint, weight, &replWeight, randomProofs(t)))

				all, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[storageprovider.Record], error) {
					var listOpts []storageprovider.ListOption
					if opts.Cursor != nil {
						listOpts = append(listOpts, storageprovider.WithListCursor(*opts.Cursor))
					}
					return s.List(ctx, listOpts...)
				})
				require.NoError(t, err)

				ids := make([]string, 0, len(all))
				for _, r := range all {
					ids = append(ids, r.Provider.String())
				}
				require.Contains(t, ids, provider1.DID().String())
				require.Contains(t, ids, provider2.DID().String())
			})

			t.Run("List paginates results", func(t *testing.T) {
				weight := 10
				replWeight := 5
				for range 5 {
					provider := testutil.RandomDID(t)
					endpoint := randomEndpoint(t)
					require.NoError(t, s.Put(t.Context(), provider, endpoint, weight, &replWeight, randomProofs(t)))
				}

				all, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[storageprovider.Record], error) {
					listOpts := []storageprovider.ListOption{storageprovider.WithListLimit(2)}
					if opts.Cursor != nil {
						listOpts = append(listOpts, storageprovider.WithListCursor(*opts.Cursor))
					}
					return s.List(ctx, listOpts...)
				})
				require.NoError(t, err)
				require.GreaterOrEqual(t, len(all), 5)
			})

			t.Run("deleted provider does not appear in List", func(t *testing.T) {
				provider := testutil.Alice
				endpoint := randomEndpoint(t)
				weight := 10
				replWeight := 5

				require.NoError(t, s.Put(t.Context(), provider.DID(), endpoint, weight, &replWeight, randomProofs(t)))
				require.NoError(t, s.Delete(t.Context(), provider.DID()))

				all, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[storageprovider.Record], error) {
					var listOpts []storageprovider.ListOption
					if opts.Cursor != nil {
						listOpts = append(listOpts, storageprovider.WithListCursor(*opts.Cursor))
					}
					return s.List(ctx, listOpts...)
				})
				require.NoError(t, err)

				for _, r := range all {
					require.NotEqual(t, provider.DID(), r.Provider)
				}
			})
		})
	}
}
