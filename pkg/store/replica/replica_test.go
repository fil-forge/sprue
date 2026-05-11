package replica_test

import (
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store/replica"
	replicaaws "github.com/fil-forge/sprue/pkg/store/replica/aws"
	"github.com/fil-forge/sprue/pkg/store/replica/memory"
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

func makeStore(t *testing.T, k StoreKind) replica.Store {
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

func createPostgresStore(t *testing.T) replica.Store {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	return replicapostgres.New(pool)
}

func createAWSStore(t *testing.T) replica.Store {
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

	s := replicaaws.New(dynamo, "replica-"+uuid.NewString())
	require.NoError(t, s.Initialize(t.Context()))
	return s
}

func TestReplicaStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			s := makeStore(t, k)

			t.Run("adds a replica", func(t *testing.T) {
				space := testutil.RandomDID(t)
				digest := testutil.RandomMultihash(t)
				provider := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				err := s.Add(t.Context(), space, digest, provider, replica.Allocated, cause)
				require.NoError(t, err)

				records, err := s.List(t.Context(), space, digest)
				require.NoError(t, err)
				require.Len(t, records, 1)
				require.Equal(t, space, records[0].Space)
				require.Equal(t, digest, records[0].Digest)
				require.Equal(t, provider, records[0].Provider)
				require.Equal(t, replica.Allocated, records[0].Status)
				require.Equal(t, cause, records[0].Cause)
				require.False(t, records[0].CreatedAt.IsZero())
			})

			t.Run("returns empty list for unknown space", func(t *testing.T) {
				space := testutil.RandomDID(t)
				digest := testutil.RandomMultihash(t)

				records, err := s.List(t.Context(), space, digest)
				require.NoError(t, err)
				require.Empty(t, records)
			})

			t.Run("returns empty list for unknown digest", func(t *testing.T) {
				space := testutil.RandomDID(t)
				digest := testutil.RandomMultihash(t)
				otherDigest := testutil.RandomMultihash(t)
				provider := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				err := s.Add(t.Context(), space, digest, provider, replica.Allocated, cause)
				require.NoError(t, err)

				records, err := s.List(t.Context(), space, otherDigest)
				require.NoError(t, err)
				require.Empty(t, records)
			})

			t.Run("returns error when adding duplicate replica", func(t *testing.T) {
				space := testutil.RandomDID(t)
				digest := testutil.RandomMultihash(t)
				provider := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				err := s.Add(t.Context(), space, digest, provider, replica.Allocated, cause)
				require.NoError(t, err)

				err = s.Add(t.Context(), space, digest, provider, replica.Allocated, cause)
				require.ErrorIs(t, err, replica.ErrReplicaExists)
			})

			t.Run("lists multiple replicas for a blob", func(t *testing.T) {
				space := testutil.RandomDID(t)
				digest := testutil.RandomMultihash(t)
				provider1 := testutil.RandomDID(t)
				provider2 := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				require.NoError(t, s.Add(t.Context(), space, digest, provider1, replica.Allocated, cause))
				require.NoError(t, s.Add(t.Context(), space, digest, provider2, replica.Transferred, cause))

				records, err := s.List(t.Context(), space, digest)
				require.NoError(t, err)
				require.Len(t, records, 2)

				providers := []any{records[0].Provider, records[1].Provider}
				require.ElementsMatch(t, []any{provider1, provider2}, providers)
			})

			t.Run("isolates replicas by space", func(t *testing.T) {
				space1 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)
				digest := testutil.RandomMultihash(t)
				provider := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				require.NoError(t, s.Add(t.Context(), space1, digest, provider, replica.Allocated, cause))

				records, err := s.List(t.Context(), space2, digest)
				require.NoError(t, err)
				require.Empty(t, records)
			})

			t.Run("sets status of a replica", func(t *testing.T) {
				space := testutil.RandomDID(t)
				digest := testutil.RandomMultihash(t)
				provider := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				require.NoError(t, s.Add(t.Context(), space, digest, provider, replica.Allocated, cause))

				err := s.SetStatus(t.Context(), space, digest, provider, replica.Transferred)
				require.NoError(t, err)

				records, err := s.List(t.Context(), space, digest)
				require.NoError(t, err)
				require.Len(t, records, 1)
				require.Equal(t, replica.Transferred, records[0].Status)
				require.False(t, records[0].UpdatedAt.IsZero())
			})

			t.Run("returns error when setting status of unknown replica", func(t *testing.T) {
				space := testutil.RandomDID(t)
				digest := testutil.RandomMultihash(t)
				provider := testutil.RandomDID(t)

				err := s.SetStatus(t.Context(), space, digest, provider, replica.Transferred)
				require.ErrorIs(t, err, replica.ErrReplicaNotFound)
			})

			t.Run("retries a replica", func(t *testing.T) {
				space := testutil.RandomDID(t)
				digest := testutil.RandomMultihash(t)
				provider := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)
				newCause := testutil.RandomCID(t)

				require.NoError(t, s.Add(t.Context(), space, digest, provider, replica.Failed, cause))

				err := s.Retry(t.Context(), space, digest, provider, replica.Allocated, newCause)
				require.NoError(t, err)

				records, err := s.List(t.Context(), space, digest)
				require.NoError(t, err)
				require.Len(t, records, 1)
				require.Equal(t, replica.Allocated, records[0].Status)
				require.Equal(t, newCause, records[0].Cause)
				require.False(t, records[0].UpdatedAt.IsZero())
			})

			t.Run("returns error when retrying unknown replica", func(t *testing.T) {
				space := testutil.RandomDID(t)
				digest := testutil.RandomMultihash(t)
				provider := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				err := s.Retry(t.Context(), space, digest, provider, replica.Allocated, cause)
				require.ErrorIs(t, err, replica.ErrReplicaNotFound)
			})
		})
	}
}
