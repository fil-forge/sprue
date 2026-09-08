package routingpolicy_test

import (
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	routingpolicy "github.com/fil-forge/sprue/pkg/store/routing_policy"
	routingpolicymemory "github.com/fil-forge/sprue/pkg/store/routing_policy/memory"
	routingpolicypostgres "github.com/fil-forge/sprue/pkg/store/routing_policy/postgres"
	"github.com/fil-forge/ucantone/did"
	"github.com/stretchr/testify/require"
)

type StoreKind string

const (
	Memory   StoreKind = "memory"
	Postgres StoreKind = "postgres"
)

var storeKinds = []StoreKind{Memory, Postgres}

func makeStore(t *testing.T, k StoreKind) routingpolicy.Store {
	switch k {
	case Memory:
		return routingpolicymemory.New()
	case Postgres:
		return createPostgresStore(t)
	}
	panic("unknown store kind")
}

func createPostgresStore(t *testing.T) routingpolicy.Store {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	return routingpolicypostgres.New(pool)
}

func TestRoutingPolicyStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			s := makeStore(t, k)
			ctx := t.Context()

			t.Run("sets and gets candidates", func(t *testing.T) {
				policy := testutil.RandomDID(t)
				nodeA, nodeB := testutil.RandomDID(t), testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				require.NoError(t, s.SetCandidates(ctx, policy, []did.DID{nodeA, nodeB}, cause))

				rec, err := s.GetPolicy(ctx, policy)
				require.NoError(t, err)
				require.Equal(t, policy, rec.Policy)
				require.ElementsMatch(t, []did.DID{nodeA, nodeB}, rec.Candidates)
				require.Equal(t, cause, rec.Cause)
				require.False(t, rec.InsertedAt.IsZero())
				require.False(t, rec.UpdatedAt.IsZero())
			})

			t.Run("replaces candidates", func(t *testing.T) {
				policy := testutil.RandomDID(t)
				nodeA, nodeB := testutil.RandomDID(t), testutil.RandomDID(t)

				require.NoError(t, s.SetCandidates(ctx, policy, []did.DID{nodeA}, testutil.RandomCID(t)))
				require.NoError(t, s.SetCandidates(ctx, policy, []did.DID{nodeB}, testutil.RandomCID(t)))

				rec, err := s.GetPolicy(ctx, policy)
				require.NoError(t, err)
				require.Equal(t, []did.DID{nodeB}, rec.Candidates)
			})

			t.Run("rejects empty candidates", func(t *testing.T) {
				require.Error(t, s.SetCandidates(ctx, testutil.RandomDID(t), nil, testutil.RandomCID(t)))
			})

			t.Run("unknown policy", func(t *testing.T) {
				_, err := s.GetPolicy(ctx, testutil.RandomDID(t))
				require.ErrorIs(t, err, routingpolicy.ErrPolicyNotFound)
			})

			t.Run("sets and gets space policy", func(t *testing.T) {
				policy := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				require.NoError(t, s.SetCandidates(ctx, policy, []did.DID{testutil.RandomDID(t)}, testutil.RandomCID(t)))

				cause := testutil.RandomCID(t)
				require.NoError(t, s.SetSpacePolicy(ctx, space, policy, cause))

				rec, err := s.GetSpacePolicy(ctx, space)
				require.NoError(t, err)
				require.Equal(t, space, rec.Space)
				require.Equal(t, policy, rec.Policy)
				require.Equal(t, cause, rec.Cause)
			})

			t.Run("replaces space policy", func(t *testing.T) {
				policyA, policyB := testutil.RandomDID(t), testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				require.NoError(t, s.SetCandidates(ctx, policyA, []did.DID{testutil.RandomDID(t)}, testutil.RandomCID(t)))
				require.NoError(t, s.SetCandidates(ctx, policyB, []did.DID{testutil.RandomDID(t)}, testutil.RandomCID(t)))

				require.NoError(t, s.SetSpacePolicy(ctx, space, policyA, testutil.RandomCID(t)))
				require.NoError(t, s.SetSpacePolicy(ctx, space, policyB, testutil.RandomCID(t)))

				rec, err := s.GetSpacePolicy(ctx, space)
				require.NoError(t, err)
				require.Equal(t, policyB, rec.Policy)
			})

			t.Run("space cannot reference unknown policy", func(t *testing.T) {
				err := s.SetSpacePolicy(ctx, testutil.RandomDID(t), testutil.RandomDID(t), testutil.RandomCID(t))
				require.ErrorIs(t, err, routingpolicy.ErrPolicyNotFound)
			})

			t.Run("clears space policy", func(t *testing.T) {
				policy := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				require.NoError(t, s.SetCandidates(ctx, policy, []did.DID{testutil.RandomDID(t)}, testutil.RandomCID(t)))
				require.NoError(t, s.SetSpacePolicy(ctx, space, policy, testutil.RandomCID(t)))

				require.NoError(t, s.ClearSpacePolicy(ctx, space))
				_, err := s.GetSpacePolicy(ctx, space)
				require.ErrorIs(t, err, routingpolicy.ErrSpacePolicyNotFound)

				// Clearing again is a no-op.
				require.NoError(t, s.ClearSpacePolicy(ctx, space))
			})

			t.Run("unknown space policy", func(t *testing.T) {
				_, err := s.GetSpacePolicy(ctx, testutil.RandomDID(t))
				require.ErrorIs(t, err, routingpolicy.ErrSpacePolicyNotFound)
			})
		})
	}
}
