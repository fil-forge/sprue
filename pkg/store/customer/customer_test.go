package customer_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/customer"
	customeraws "github.com/fil-forge/sprue/pkg/store/customer/aws"
	customermemory "github.com/fil-forge/sprue/pkg/store/customer/memory"
	customerpostgres "github.com/fil-forge/sprue/pkg/store/customer/postgres"
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

func makeStore(t *testing.T, k StoreKind) customer.Store {
	switch k {
	case Memory:
		return customermemory.New()
	case AWS:
		return createAWSStore(t)
	case Postgres:
		return createPostgresStore(t)
	}
	panic("unknown store kind")
}

func createPostgresStore(t *testing.T) customer.Store {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	return customerpostgres.New(pool)
}

func createAWSStore(t *testing.T) customer.Store {
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

	s := customeraws.New(dynamo, "customer-"+uuid.NewString())
	require.NoError(t, s.Initialize(t.Context()))
	return s
}

func TestCustomerStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			s := makeStore(t, k)

			t.Run("adds and gets a customer", func(t *testing.T) {
				customerID := testutil.RandomDID(t)
				product := testutil.RandomDID(t)

				require.NoError(t, s.Add(t.Context(), customerID, nil, product, nil, nil))

				rec, err := s.Get(t.Context(), customerID)
				require.NoError(t, err)
				require.Equal(t, customerID, rec.Customer)
				require.Equal(t, product, rec.Product)
				require.Nil(t, rec.Account)
				require.False(t, rec.InsertedAt.IsZero())
			})

			t.Run("Add stores optional fields", func(t *testing.T) {
				customerID := testutil.RandomDID(t)
				account := "xyz:" + uuid.NewString()
				product := testutil.RandomDID(t)
				var capacity uint64 = 1024

				require.NoError(t, s.Add(t.Context(), customerID, &account, product, map[string]any{"key": "val"}, &capacity))

				rec, err := s.Get(t.Context(), customerID)
				require.NoError(t, err)
				require.Equal(t, &account, rec.Account)
				require.Equal(t, &capacity, rec.ReservedCapacity)
			})

			t.Run("Add returns ErrCustomerExists for duplicate customer", func(t *testing.T) {
				customerID := testutil.RandomDID(t)
				product := testutil.RandomDID(t)

				require.NoError(t, s.Add(t.Context(), customerID, nil, product, nil, nil))
				err := s.Add(t.Context(), customerID, nil, product, nil, nil)
				require.ErrorIs(t, err, customer.ErrCustomerExists)
			})

			t.Run("Get returns ErrCustomerNotFound for unknown customer", func(t *testing.T) {
				_, err := s.Get(t.Context(), testutil.RandomDID(t))
				require.ErrorIs(t, err, customer.ErrCustomerNotFound)
			})

			t.Run("UpdateProduct updates the product", func(t *testing.T) {
				customerID := testutil.RandomDID(t)
				product1 := testutil.RandomDID(t)
				product2 := testutil.RandomDID(t)

				require.NoError(t, s.Add(t.Context(), customerID, nil, product1, nil, nil))
				require.NoError(t, s.UpdateProduct(t.Context(), customerID, product2))

				rec, err := s.Get(t.Context(), customerID)
				require.NoError(t, err)
				require.Equal(t, product2, rec.Product)
			})

			t.Run("UpdateProduct returns ErrCustomerNotFound for unknown customer", func(t *testing.T) {
				err := s.UpdateProduct(t.Context(), testutil.RandomDID(t), testutil.RandomDID(t))
				require.ErrorIs(t, err, customer.ErrCustomerNotFound)
			})

			t.Run("List returns all customers", func(t *testing.T) {
				product := testutil.RandomDID(t)
				c1 := testutil.RandomDID(t)
				c2 := testutil.RandomDID(t)

				require.NoError(t, s.Add(t.Context(), c1, nil, product, nil, nil))
				require.NoError(t, s.Add(t.Context(), c2, nil, product, nil, nil))

				all, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[customer.Record], error) {
					var listOpts []customer.ListOption
					if opts.Cursor != nil {
						listOpts = append(listOpts, customer.WithListCursor(*opts.Cursor))
					}
					return s.List(t.Context(), listOpts...)
				})
				require.NoError(t, err)

				ids := make([]string, 0, len(all))
				for _, r := range all {
					ids = append(ids, r.Customer.String())
				}
				require.Contains(t, ids, c1.String())
				require.Contains(t, ids, c2.String())
			})

			t.Run("List paginates results", func(t *testing.T) {
				product := testutil.RandomDID(t)
				for range 5 {
					require.NoError(t, s.Add(t.Context(), testutil.RandomDID(t), nil, product, nil, nil))
				}

				all, err := store.Collect(t.Context(), func(ctx context.Context, opts store.PaginationConfig) (store.Page[customer.Record], error) {
					var listOpts []customer.ListOption
					if opts.Cursor != nil {
						listOpts = append(listOpts, customer.WithListCursor(*opts.Cursor))
					}
					listOpts = append(listOpts, customer.WithListLimit(2))
					return s.List(t.Context(), listOpts...)
				})
				require.NoError(t, err)
				require.GreaterOrEqual(t, len(all), 5)
			})
		})
	}
}
