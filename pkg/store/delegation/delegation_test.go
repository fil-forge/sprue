package delegation_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store"
	dlgstore "github.com/fil-forge/sprue/pkg/store/delegation"
	delegationaws "github.com/fil-forge/sprue/pkg/store/delegation/aws"
	"github.com/fil-forge/sprue/pkg/store/delegation/memory"
	"github.com/fil-forge/ucantone/ucan"
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

func makeStore(t *testing.T, k StoreKind) dlgstore.Store {
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

func createPostgresStore(t *testing.T) dlgstore.Store {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	s3Endpoint := testutil.CreateS3(t)
	s3Client := testutil.NewS3Client(t, s3Endpoint)

	s := delegationpostgres.New(pool, s3Client, "delegation-"+uuid.NewString())
	require.NoError(t, s.Initialize(t.Context()))
	return s
}

func createAWSStore(t *testing.T) dlgstore.Store {
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

	suffix := uuid.NewString()

	dynamoEndpoint := testutil.CreateDynamo(t)
	dynamo := testutil.NewDynamoClient(t, dynamoEndpoint)

	s3Endpoint := testutil.CreateS3(t)
	s3Client := testutil.NewS3Client(t, s3Endpoint)

	s := delegationaws.New(dynamo, "delegation-"+suffix, s3Client, "delegation-"+suffix)
	require.NoError(t, s.Initialize(t.Context()))
	return s
}

// makeDelegation creates a delegation from Alice to the given audience.
func makeDelegation(t *testing.T, audience ucan.Principal) ucan.Delegation {
	t.Helper()
	dlg, err := delegation.Delegate(
		testutil.Alice,
		audience,
		testutil.Alice,
		"/test/delegate",
	)
	require.NoError(t, err)
	return dlg
}

func TestDelegationStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			s := makeStore(t, k)

			t.Run("stores and retrieves a delegation", func(t *testing.T) {
				audience := testutil.RandomDID(t)
				dlg := makeDelegation(t, audience)
				cause := testutil.RandomCID(t)

				require.NoError(t, s.PutMany(t.Context(), []ucan.Token{dlg}, cause))

				page, err := s.ListByAudience(t.Context(), audience.DID())
				require.NoError(t, err)
				require.Len(t, page.Results, 1)
				require.Equal(t, dlg.Link().String(), page.Results[0].Link().String())
			})

			t.Run("ListByAudience returns empty page for unknown audience", func(t *testing.T) {
				audience := testutil.RandomDID(t)

				page, err := s.ListByAudience(t.Context(), audience.DID())
				require.NoError(t, err)
				require.Empty(t, page.Results)
				require.Nil(t, page.Cursor)
			})

			t.Run("PutMany stores multiple delegations for the same audience", func(t *testing.T) {
				audience := testutil.RandomDID(t)
				dlg1 := makeDelegation(t, audience)
				dlg2 := makeDelegation(t, audience)
				cause := testutil.RandomCID(t)

				require.NoError(t, s.PutMany(t.Context(), []ucan.Token{dlg1, dlg2}, cause))

				page, err := s.ListByAudience(t.Context(), audience.DID())
				require.NoError(t, err)
				require.Len(t, page.Results, 2)
			})

			t.Run("PutMany stores delegations across multiple audiences", func(t *testing.T) {
				aud1 := testutil.RandomDID(t)
				aud2 := testutil.RandomDID(t)
				dlg1 := makeDelegation(t, aud1)
				dlg2 := makeDelegation(t, aud2)
				cause := testutil.RandomCID(t)

				require.NoError(t, s.PutMany(t.Context(), []ucan.Token{dlg1, dlg2}, cause))

				page1, err := s.ListByAudience(t.Context(), aud1.DID())
				require.NoError(t, err)
				require.Len(t, page1.Results, 1)
				require.Equal(t, dlg1.Link().String(), page1.Results[0].Link().String())

				page2, err := s.ListByAudience(t.Context(), aud2.DID())
				require.NoError(t, err)
				require.Len(t, page2.Results, 1)
				require.Equal(t, dlg2.Link().String(), page2.Results[0].Link().String())
			})

			t.Run("ListByAudience isolates delegations by audience", func(t *testing.T) {
				aud1 := testutil.RandomDID(t)
				aud2 := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				for range 3 {
					require.NoError(t, s.PutMany(t.Context(), []ucan.Token{makeDelegation(t, aud1)}, cause))
				}
				require.NoError(t, s.PutMany(t.Context(), []ucan.Token{makeDelegation(t, aud2)}, cause))

				page, err := s.ListByAudience(t.Context(), aud1.DID())
				require.NoError(t, err)
				require.Len(t, page.Results, 3)
			})

			t.Run("ListByAudience paginates results", func(t *testing.T) {
				audience := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				for range 5 {
					require.NoError(t, s.PutMany(t.Context(), []ucan.Token{makeDelegation(t, audience)}, cause))
				}

				all, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[ucan.Token], error) {
					var listOpts []dlgstore.ListByAudienceOption
					if opts.Cursor != nil {
						listOpts = append(listOpts, dlgstore.WithListByAudienceCursor(*opts.Cursor))
					}
					listOpts = append(listOpts, dlgstore.WithListByAudienceLimit(2))
					return s.ListByAudience(ctx, audience.DID(), listOpts...)
				})
				require.NoError(t, err)
				require.Len(t, all, 5)
			})
		})
	}
}
