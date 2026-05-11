package revocation_test

import (
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store/revocation"
	revocationaws "github.com/fil-forge/sprue/pkg/store/revocation/aws"
	"github.com/fil-forge/sprue/pkg/store/revocation/memory"
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

func makeStore(t *testing.T, k StoreKind) revocation.Store {
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

func createPostgresStore(t *testing.T) revocation.Store {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	return revocationpostgres.New(pool)
}

func createAWSStore(t *testing.T) revocation.Store {
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

	s := revocationaws.New(dynamo, "revocation-"+uuid.NewString())
	require.NoError(t, s.Initialize(t.Context()))
	return s
}

func TestRevocationStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			store := makeStore(t, k)

			t.Run("find returns empty map when no revocations exist", func(t *testing.T) {
				delegation := testutil.RandomCID(t)

				result, err := store.Find(t.Context(), []cid.Cid{delegation})
				require.NoError(t, err)
				require.Empty(t, result)
			})

			t.Run("adds a revocation", func(t *testing.T) {
				delegation := testutil.RandomCID(t)
				scope := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				err := store.Add(t.Context(), delegation, scope, cause)
				require.NoError(t, err)

				result, err := store.Find(t.Context(), []cid.Cid{delegation})
				require.NoError(t, err)
				require.Len(t, result, 1)
				require.Contains(t, result, delegation)
				require.Equal(t, cause, result[delegation][scope])
			})

			t.Run("add does not overwrite existing revocation for same scope", func(t *testing.T) {
				delegation := testutil.RandomCID(t)
				scope := testutil.RandomDID(t)
				cause1 := testutil.RandomCID(t)
				cause2 := testutil.RandomCID(t)

				err := store.Add(t.Context(), delegation, scope, cause1)
				require.NoError(t, err)

				err = store.Add(t.Context(), delegation, scope, cause2)
				require.NoError(t, err)

				result, err := store.Find(t.Context(), []cid.Cid{delegation})
				require.NoError(t, err)
				require.Equal(t, cause1, result[delegation][scope])
			})

			t.Run("add accumulates revocations for different scopes", func(t *testing.T) {
				delegation := testutil.RandomCID(t)
				scope1 := testutil.RandomDID(t)
				scope2 := testutil.RandomDID(t)
				cause1 := testutil.RandomCID(t)
				cause2 := testutil.RandomCID(t)

				err := store.Add(t.Context(), delegation, scope1, cause1)
				require.NoError(t, err)

				err = store.Add(t.Context(), delegation, scope2, cause2)
				require.NoError(t, err)

				result, err := store.Find(t.Context(), []cid.Cid{delegation})
				require.NoError(t, err)
				require.Len(t, result[delegation], 2)
				require.Equal(t, cause1, result[delegation][scope1])
				require.Equal(t, cause2, result[delegation][scope2])
			})

			t.Run("find returns revocations for multiple delegations", func(t *testing.T) {
				delegation1 := testutil.RandomCID(t)
				delegation2 := testutil.RandomCID(t)
				delegation3 := testutil.RandomCID(t)
				scope := testutil.RandomDID(t)
				cause1 := testutil.RandomCID(t)
				cause2 := testutil.RandomCID(t)

				require.NoError(t, store.Add(t.Context(), delegation1, scope, cause1))
				require.NoError(t, store.Add(t.Context(), delegation2, scope, cause2))

				result, err := store.Find(t.Context(), []cid.Cid{delegation1, delegation2, delegation3})
				require.NoError(t, err)
				require.Len(t, result, 2)
				require.Contains(t, result, delegation1)
				require.Contains(t, result, delegation2)
				require.NotContains(t, result, delegation3)
			})

			t.Run("find with empty list returns empty map", func(t *testing.T) {
				result, err := store.Find(t.Context(), []cid.Cid{})
				require.NoError(t, err)
				require.Empty(t, result)
			})

			t.Run("reset replaces all revocations for a delegation", func(t *testing.T) {
				delegation := testutil.RandomCID(t)
				scope1 := testutil.RandomDID(t)
				scope2 := testutil.RandomDID(t)
				cause1 := testutil.RandomCID(t)
				cause2 := testutil.RandomCID(t)
				newCause := testutil.RandomCID(t)

				require.NoError(t, store.Add(t.Context(), delegation, scope1, cause1))
				require.NoError(t, store.Add(t.Context(), delegation, scope2, cause2))

				err := store.Reset(t.Context(), delegation, scope1, newCause)
				require.NoError(t, err)

				result, err := store.Find(t.Context(), []cid.Cid{delegation})
				require.NoError(t, err)
				require.Len(t, result[delegation], 1)
				require.Equal(t, newCause, result[delegation][scope1])
				require.NotContains(t, result[delegation], scope2)
			})

			t.Run("reset creates a revocation when none exists", func(t *testing.T) {
				delegation := testutil.RandomCID(t)
				scope := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				err := store.Reset(t.Context(), delegation, scope, cause)
				require.NoError(t, err)

				result, err := store.Find(t.Context(), []cid.Cid{delegation})
				require.NoError(t, err)
				require.Len(t, result[delegation], 1)
				require.Equal(t, cause, result[delegation][scope])
			})

			t.Run("reset does not affect other delegations", func(t *testing.T) {
				delegation1 := testutil.RandomCID(t)
				delegation2 := testutil.RandomCID(t)
				scope := testutil.RandomDID(t)
				cause1 := testutil.RandomCID(t)
				cause2 := testutil.RandomCID(t)
				newCause := testutil.RandomCID(t)

				require.NoError(t, store.Add(t.Context(), delegation1, scope, cause1))
				require.NoError(t, store.Add(t.Context(), delegation2, scope, cause2))

				require.NoError(t, store.Reset(t.Context(), delegation1, scope, newCause))

				result, err := store.Find(t.Context(), []cid.Cid{delegation1, delegation2})
				require.NoError(t, err)
				require.Equal(t, newCause, result[delegation1][scope])
				require.Equal(t, cause2, result[delegation2][scope])
			})
		})
	}
}
