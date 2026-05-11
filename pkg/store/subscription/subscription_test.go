package subscription_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/subscription"
	subscriptionaws "github.com/fil-forge/sprue/pkg/store/subscription/aws"
	subscriptionmemory "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	subscriptionpostgres "github.com/fil-forge/sprue/pkg/store/subscription/postgres"
	"github.com/fil-forge/ucantone/did"
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

func makeStore(t *testing.T, k StoreKind) subscription.Store {
	switch k {
	case Memory:
		return subscriptionmemory.New()
	case AWS:
		return createAWSStore(t)
	case Postgres:
		return createPostgresStore(t)
	}
	panic("unknown store kind")
}

func createPostgresStore(t *testing.T) subscription.Store {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	return subscriptionpostgres.New(pool)
}

func createAWSStore(t *testing.T) subscription.Store {
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

	s := subscriptionaws.New(dynamo, "subscription-"+uuid.NewString())
	err := s.Initialize(t.Context())
	require.NoError(t, err)
	return s
}

func listAllSubscriptions(t *testing.T, s subscription.Store, provider, customer did.DID) []subscription.Record {
	t.Helper()
	subs, err := store.Collect(t.Context(), func(ctx context.Context, options store.PaginationConfig) (store.Page[subscription.Record], error) {
		opts := []subscription.ListByProviderAndCustomerOption{subscription.WithListByProviderAndCustomerLimit(1000)}
		if options.Cursor != nil {
			opts = append(opts, subscription.WithListByProviderAndCustomerCursor(*options.Cursor))
		}
		return s.ListByProviderAndCustomer(ctx, provider, customer, opts...)
	})
	require.NoError(t, err)
	return subs
}

func TestSubscriptionStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			s := makeStore(t, k)

			t.Run("adds a subscription", func(t *testing.T) {
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				subscriptionID := uuid.NewString()
				cause := testutil.RandomCID(t)

				err := s.Add(t.Context(), provider, subscriptionID, customer, cause)
				require.NoError(t, err)

				rec, err := s.Get(t.Context(), provider, subscriptionID)
				require.NoError(t, err)
				require.Equal(t, provider, rec.Provider)
				require.Equal(t, subscriptionID, rec.Subscription)
				require.Equal(t, customer, rec.Customer)
				require.Equal(t, cause, rec.Cause)
			})

			t.Run("returns error when adding duplicate subscription", func(t *testing.T) {
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				subscriptionID := uuid.NewString()
				cause := testutil.RandomCID(t)

				err := s.Add(t.Context(), provider, subscriptionID, customer, cause)
				require.NoError(t, err)

				err = s.Add(t.Context(), provider, subscriptionID, customer, cause)
				require.ErrorIs(t, err, subscription.ErrSubscriptionExists)
			})

			t.Run("returns error when getting non-existent subscription", func(t *testing.T) {
				provider := testutil.RandomDID(t)

				_, err := s.Get(t.Context(), provider, uuid.NewString())
				require.ErrorIs(t, err, subscription.ErrSubscriptionNotFound)
			})

			t.Run("subscriptions are scoped to provider", func(t *testing.T) {
				provider1 := testutil.RandomDID(t)
				provider2 := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				subscriptionID := uuid.NewString()
				cause := testutil.RandomCID(t)

				err := s.Add(t.Context(), provider1, subscriptionID, customer, cause)
				require.NoError(t, err)

				// same subscription ID under a different provider should not exist
				_, err = s.Get(t.Context(), provider2, subscriptionID)
				require.ErrorIs(t, err, subscription.ErrSubscriptionNotFound)

				// and can be added independently
				err = s.Add(t.Context(), provider2, subscriptionID, customer, cause)
				require.NoError(t, err)
			})

			t.Run("lists subscriptions by provider and customer", func(t *testing.T) {
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
				for _, id := range ids {
					err := s.Add(t.Context(), provider, id, customer, cause)
					require.NoError(t, err)
				}

				page, err := s.ListByProviderAndCustomer(t.Context(), provider, customer)
				require.NoError(t, err)
				require.Len(t, page.Results, 3)
				for _, rec := range page.Results {
					require.Equal(t, provider, rec.Provider)
					require.Equal(t, customer, rec.Customer)
				}
			})

			t.Run("list only returns subscriptions for the given customer", func(t *testing.T) {
				provider := testutil.RandomDID(t)
				customer1 := testutil.RandomDID(t)
				customer2 := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				err := s.Add(t.Context(), provider, uuid.NewString(), customer1, cause)
				require.NoError(t, err)
				err = s.Add(t.Context(), provider, uuid.NewString(), customer1, cause)
				require.NoError(t, err)
				err = s.Add(t.Context(), provider, uuid.NewString(), customer2, cause)
				require.NoError(t, err)

				page, err := s.ListByProviderAndCustomer(t.Context(), provider, customer1)
				require.NoError(t, err)
				require.Len(t, page.Results, 2)

				page, err = s.ListByProviderAndCustomer(t.Context(), provider, customer2)
				require.NoError(t, err)
				require.Len(t, page.Results, 1)
			})

			t.Run("list returns empty page for unknown provider or customer", func(t *testing.T) {
				page, err := s.ListByProviderAndCustomer(t.Context(), testutil.RandomDID(t), testutil.RandomDID(t))
				require.NoError(t, err)
				require.Empty(t, page.Results)
				require.Nil(t, page.Cursor)
			})

			t.Run("list paginates with limit and cursor", func(t *testing.T) {
				provider := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				for range 3 {
					err := s.Add(t.Context(), provider, uuid.NewString(), customer, cause)
					require.NoError(t, err)
				}

				// first page
				limit := 2
				page, err := s.ListByProviderAndCustomer(t.Context(), provider, customer, subscription.WithListByProviderAndCustomerLimit(limit))
				require.NoError(t, err)
				require.Len(t, page.Results, 2)
				require.NotNil(t, page.Cursor)

				// second page
				page, err = s.ListByProviderAndCustomer(t.Context(), provider, customer, subscription.WithListByProviderAndCustomerCursor(*page.Cursor))
				require.NoError(t, err)
				require.Len(t, page.Results, 1)

				// collect all via pagination helper
				all := listAllSubscriptions(t, s, provider, customer)
				require.Len(t, all, 3)
			})
		})
	}
}
