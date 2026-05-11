package consumer_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	consumeraws "github.com/fil-forge/sprue/pkg/store/consumer/aws"
	consumermemory "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	consumerpostgres "github.com/fil-forge/sprue/pkg/store/consumer/postgres"
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

func makeStore(t *testing.T, k StoreKind) consumer.Store {
	switch k {
	case Memory:
		return consumermemory.New()
	case AWS:
		return createAWSStore(t)
	case Postgres:
		return createPostgresStore(t)
	}
	panic("unknown store kind")
}

func createPostgresStore(t *testing.T) consumer.Store {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	return consumerpostgres.New(pool)
}

func createAWSStore(t *testing.T) *consumeraws.Store {
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

	s := consumeraws.New(dynamo, "consumer-"+uuid.NewString())
	require.NoError(t, s.Initialize(t.Context()))
	return s
}

func TestConsumerStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			s := makeStore(t, k)

			t.Run("adds a consumer", func(t *testing.T) {
				provider := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				err := s.Add(t.Context(), provider, space, customer, "sub1", cause)
				require.NoError(t, err)

				rec, err := s.Get(t.Context(), provider, space)
				require.NoError(t, err)
				require.Equal(t, provider, rec.Provider)
				require.Equal(t, space, rec.Consumer)
				require.Equal(t, customer, rec.Customer)
				require.Equal(t, "sub1", rec.Subscription)
				require.Equal(t, cause, rec.Cause)
			})

			t.Run("returns ErrConsumerExists when adding a duplicate", func(t *testing.T) {
				provider := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				require.NoError(t, s.Add(t.Context(), provider, space, customer, "sub1", cause))

				err := s.Add(t.Context(), provider, space, customer, "sub1", cause)
				require.ErrorIs(t, err, consumer.ErrConsumerExists)
			})

			t.Run("get returns ErrConsumerNotFound for unknown provider/space", func(t *testing.T) {
				provider := testutil.RandomDID(t)
				space := testutil.RandomDID(t)

				_, err := s.Get(t.Context(), provider, space)
				require.ErrorIs(t, err, consumer.ErrConsumerNotFound)
			})

			t.Run("gets by subscription", func(t *testing.T) {
				provider := testutil.RandomDID(t)
				space := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				require.NoError(t, s.Add(t.Context(), provider, space, customer, "sub1", cause))

				rec, err := s.GetBySubscription(t.Context(), provider, "sub1")
				require.NoError(t, err)
				require.Equal(t, provider, rec.Provider)
				require.Equal(t, space, rec.Consumer)
				require.Equal(t, "sub1", rec.Subscription)
			})

			t.Run("GetBySubscription returns ErrConsumerNotFound for unknown subscription", func(t *testing.T) {
				provider := testutil.RandomDID(t)

				_, err := s.GetBySubscription(t.Context(), provider, "sub-missing")
				require.ErrorIs(t, err, consumer.ErrConsumerNotFound)
			})

			t.Run("lists consumers for a space", func(t *testing.T) {
				space := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				// add two consumers for different providers on the same space
				provider1 := testutil.RandomDID(t)
				provider2 := testutil.RandomDID(t)
				require.NoError(t, s.Add(t.Context(), provider1, space, customer, "sub1", cause))
				require.NoError(t, s.Add(t.Context(), provider2, space, customer, "sub2", cause))

				page, err := s.List(t.Context(), space)
				require.NoError(t, err)
				require.Len(t, page.Results, 2)
			})

			t.Run("List returns empty page for unknown space", func(t *testing.T) {
				space := testutil.RandomDID(t)

				page, err := s.List(t.Context(), space)
				require.NoError(t, err)
				require.Empty(t, page.Results)
			})

			t.Run("lists consumers for a space with pagination", func(t *testing.T) {
				space := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				for i := range 3 {
					provider := testutil.RandomDID(t)
					require.NoError(t, s.Add(t.Context(), provider, space, customer, "sub"+string(rune('1'+i)), cause))
				}

				// collect all via store.Collect with a batch size of 2
				all, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[consumer.Record], error) {
					listOpts := []consumer.ListOption{consumer.WithListLimit(2)}
					if opts.Cursor != nil {
						listOpts = append(listOpts, consumer.WithListCursor(*opts.Cursor))
					}
					return s.List(ctx, space, listOpts...)
				})
				require.NoError(t, err)
				require.Len(t, all, 3)
			})

			t.Run("List isolates consumers between spaces", func(t *testing.T) {
				provider := testutil.RandomDID(t)
				space1 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				require.NoError(t, s.Add(t.Context(), provider, space1, customer, "sub1", cause))
				require.NoError(t, s.Add(t.Context(), provider, space2, customer, "sub2", cause))

				page1, err := s.List(t.Context(), space1)
				require.NoError(t, err)
				require.Len(t, page1.Results, 1)
				require.Equal(t, "sub1", page1.Results[0].Subscription)

				page2, err := s.List(t.Context(), space2)
				require.NoError(t, err)
				require.Len(t, page2.Results, 1)
				require.Equal(t, "sub2", page2.Results[0].Subscription)
			})

			t.Run("lists consumers by customer", func(t *testing.T) {
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				// add two consumers for the same customer across different spaces/providers
				provider1 := testutil.RandomDID(t)
				space1 := testutil.RandomDID(t)
				provider2 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)
				require.NoError(t, s.Add(t.Context(), provider1, space1, customer, "sub1", cause))
				require.NoError(t, s.Add(t.Context(), provider2, space2, customer, "sub2", cause))

				page, err := s.ListByCustomer(t.Context(), customer)
				require.NoError(t, err)
				require.Len(t, page.Results, 2)
			})

			t.Run("ListByCustomer returns empty page for unknown customer", func(t *testing.T) {
				customer := testutil.RandomDID(t)

				page, err := s.ListByCustomer(t.Context(), customer)
				require.NoError(t, err)
				require.Empty(t, page.Results)
			})

			t.Run("ListByCustomer isolates consumers between customers", func(t *testing.T) {
				provider := testutil.RandomDID(t)
				space1 := testutil.RandomDID(t)
				space2 := testutil.RandomDID(t)
				customer1 := testutil.RandomDID(t)
				customer2 := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				require.NoError(t, s.Add(t.Context(), provider, space1, customer1, "sub1", cause))
				require.NoError(t, s.Add(t.Context(), provider, space2, customer2, "sub2", cause))

				page1, err := s.ListByCustomer(t.Context(), customer1)
				require.NoError(t, err)
				require.Len(t, page1.Results, 1)
				require.Equal(t, "sub1", page1.Results[0].Subscription)

				page2, err := s.ListByCustomer(t.Context(), customer2)
				require.NoError(t, err)
				require.Len(t, page2.Results, 1)
				require.Equal(t, "sub2", page2.Results[0].Subscription)
			})

			t.Run("lists consumers by customer with pagination", func(t *testing.T) {
				customer := testutil.RandomDID(t)
				cause := testutil.RandomCID(t)

				for i := range 3 {
					provider := testutil.RandomDID(t)
					space := testutil.RandomDID(t)
					require.NoError(t, s.Add(t.Context(), provider, space, customer, "sub"+string(rune('1'+i)), cause))
				}

				all, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[consumer.Record], error) {
					listOpts := []consumer.ListByCustomerOption{consumer.WithListByCustomerLimit(2)}
					if opts.Cursor != nil {
						listOpts = append(listOpts, consumer.WithListByCustomerCursor(*opts.Cursor))
					}
					return s.ListByCustomer(ctx, customer, listOpts...)
				})
				require.NoError(t, err)
				require.Len(t, all, 3)
			})
		})
	}
}
