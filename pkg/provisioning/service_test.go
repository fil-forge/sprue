package provisioning_test

import (
	"context"
	"testing"

	"github.com/fil-forge/libforge/attestation/didmailto"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	consumermemory "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	"github.com/fil-forge/sprue/pkg/store/subscription"
	subscriptionmemory "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/stretchr/testify/require"
)

func mustMailtoDID(t *testing.T, email string) did.DID {
	t.Helper()
	d, err := didmailto.New(email)
	require.NoError(t, err)
	return d
}

type testSetup struct {
	service           *provisioning.Service
	consumerStore     *consumermemory.Store
	subscriptionStore *subscriptionmemory.Store
	provider          did.DID
	account           did.DID
}

func setup(t *testing.T) testSetup {
	t.Helper()
	consumerStore := consumermemory.New()
	subscriptionStore := subscriptionmemory.New()

	provider := testutil.Service.DID()
	account := mustMailtoDID(t, "alice@example.com")

	svc := provisioning.NewService(
		[]did.DID{provider},
		consumerStore,
		subscriptionStore,
	)

	return testSetup{
		service:           svc,
		consumerStore:     consumerStore,
		subscriptionStore: subscriptionStore,
		provider:          provider,
		account:           account,
	}
}

func TestProvision(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		s := setup(t)
		space := testutil.RandomDID(t)
		cause := testutil.RandomCID(t)

		sub, err := s.service.Provision(ctx, s.account, space, s.provider, cause)
		require.NoError(t, err)

		// Verify consumer record was created
		rec, err := s.consumerStore.Get(ctx, s.provider, space)
		require.NoError(t, err)
		require.Equal(t, s.provider, rec.Provider)
		require.Equal(t, space, rec.Consumer)
		require.Equal(t, s.account, rec.Customer)
		require.Equal(t, cause, rec.Cause)

		// Verify subscription was created
		subRec, err := s.subscriptionStore.Get(ctx, s.provider, sub)
		require.NoError(t, err)
		require.Equal(t, s.account, subRec.Customer)
	})

	t.Run("provider not allowed", func(t *testing.T) {
		s := setup(t)
		space := testutil.RandomDID(t)
		cause := testutil.RandomCID(t)
		badProvider := testutil.RandomDID(t)

		_, err := s.service.Provision(ctx, s.account, space, badProvider, cause)
		require.ErrorIs(t, err, provisioning.ErrProviderNotAllowed)
	})

	t.Run("duplicate provision returns consumer exists error", func(t *testing.T) {
		s := setup(t)
		space := testutil.RandomDID(t)
		cause := testutil.RandomCID(t)

		_, err := s.service.Provision(ctx, s.account, space, s.provider, cause)
		require.NoError(t, err)

		_, err = s.service.Provision(ctx, s.account, space, s.provider, cause)
		require.ErrorIs(t, err, consumer.ErrConsumerExists)
	})
}

func TestGetConsumer(t *testing.T) {
	ctx := context.Background()

	t.Run("found", func(t *testing.T) {
		s := setup(t)
		space := testutil.RandomDID(t)
		cause := testutil.RandomCID(t)

		_, err := s.service.Provision(ctx, s.account, space, s.provider, cause)
		require.NoError(t, err)

		rec, err := s.service.GetConsumer(ctx, s.provider, space)
		require.NoError(t, err)
		require.Equal(t, space, rec.Consumer)
		require.Equal(t, s.provider, rec.Provider)
	})

	t.Run("not found", func(t *testing.T) {
		s := setup(t)
		space := testutil.RandomDID(t)

		_, err := s.service.GetConsumer(ctx, s.provider, space)
		require.ErrorIs(t, err, consumer.ErrConsumerNotFound)
	})
}

func TestGetSubscription(t *testing.T) {
	ctx := context.Background()

	t.Run("found after provision", func(t *testing.T) {
		s := setup(t)
		space := testutil.RandomDID(t)
		cause := testutil.RandomCID(t)

		sub, err := s.service.Provision(ctx, s.account, space, s.provider, cause)
		require.NoError(t, err)

		rec, err := s.service.GetSubscription(ctx, s.provider, sub)
		require.NoError(t, err)
		require.Equal(t, s.account, rec.Customer)
		require.Equal(t, s.provider, rec.Provider)
	})

	t.Run("not found", func(t *testing.T) {
		s := setup(t)

		_, err := s.service.GetSubscription(ctx, s.provider, "nonexistent")
		require.ErrorIs(t, err, subscription.ErrSubscriptionNotFound)
	})
}

func TestListServiceProviders(t *testing.T) {
	ctx := context.Background()

	t.Run("no providers", func(t *testing.T) {
		s := setup(t)
		space := testutil.RandomDID(t)

		providers, err := s.service.ListServiceProviders(ctx, space)
		require.NoError(t, err)
		require.Empty(t, providers)
	})

	t.Run("returns provisioned providers", func(t *testing.T) {
		s := setup(t)
		space := testutil.RandomDID(t)
		cause := testutil.RandomCID(t)

		_, err := s.service.Provision(ctx, s.account, space, s.provider, cause)
		require.NoError(t, err)

		providers, err := s.service.ListServiceProviders(ctx, space)
		require.NoError(t, err)
		require.Len(t, providers, 1)
		require.Equal(t, s.provider, providers[0])
	})
}

func TestNewSubscriptionID(t *testing.T) {
	t.Run("deterministic", func(t *testing.T) {
		space := testutil.RandomDID(t)

		id1, err := provisioning.NewSubscriptionID(space)
		require.NoError(t, err)

		id2, err := provisioning.NewSubscriptionID(space)
		require.NoError(t, err)

		require.Equal(t, id1, id2)
	})

	t.Run("different spaces produce different IDs", func(t *testing.T) {
		space1 := testutil.RandomDID(t)
		space2 := testutil.RandomDID(t)

		id1, err := provisioning.NewSubscriptionID(space1)
		require.NoError(t, err)

		id2, err := provisioning.NewSubscriptionID(space2)
		require.NoError(t, err)

		require.NotEqual(t, id1, id2)
	})
}
