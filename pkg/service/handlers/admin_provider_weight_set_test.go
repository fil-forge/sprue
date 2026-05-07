package handlers_test

import (
	"net/url"
	"testing"

	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure/datamodel"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/capabilities/admin/provider"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestAdminProviderWeightSetHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("unauthorized issuer", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.AdminProviderWeightSetHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)
		unauthorizedIssuer := testutil.RandomSigner(t)

		caveats := provider.WeightSetCaveats{
			Provider:          storageProvider.DID(),
			Weight:            50,
			ReplicationWeight: 25,
		}

		inv, err := provider.WeightSet.Invoke(
			unauthorizedIssuer, uploadService,
			uploadService.DID().String(),
			caveats,
		)
		require.NoError(t, err)

		cap := provider.WeightSet.New(uploadService.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)

		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
		require.NotNil(t, model.Name)
		require.Equal(t, "Unauthorized", *model.Name)
	})

	t.Run("provider not found", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.AdminProviderWeightSetHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)

		caveats := provider.WeightSetCaveats{
			Provider:          storageProvider.DID(),
			Weight:            50,
			ReplicationWeight: 25,
		}

		inv, err := provider.WeightSet.Invoke(
			uploadService, uploadService,
			uploadService.DID().String(),
			caveats,
		)
		require.NoError(t, err)

		cap := provider.WeightSet.New(uploadService.DID().String(), caveats)

		// Provider is not registered, so Get will fail
		_, _, err = handler(ctx, cap, inv, nil)
		require.Error(t, err)
	})

	t.Run("success updates weights", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.AdminProviderWeightSetHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)
		endpoint := testutil.Must(url.Parse("https://piri.example.com"))(t)

		proof, err := delegation.Delegate(
			storageProvider, uploadService,
			[]ucan.Capability[ucan.NoCaveats]{
				ucan.NewCapability("blob/allocate", storageProvider.DID().String(), ucan.NoCaveats{}),
			},
		)
		require.NoError(t, err)

		// Pre-register the provider with initial weights
		initialReplWeight := 0
		err = spStore.Put(ctx, *endpoint, proof, 0, &initialReplWeight)
		require.NoError(t, err)

		// Set new weights
		caveats := provider.WeightSetCaveats{
			Provider:          storageProvider.DID(),
			Weight:            75,
			ReplicationWeight: 30,
		}

		inv, err := provider.WeightSet.Invoke(
			uploadService, uploadService,
			uploadService.DID().String(),
			caveats,
		)
		require.NoError(t, err)

		cap := provider.WeightSet.New(uploadService.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		o, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.NotNil(t, o)

		// Verify weights were updated
		rec, err := spStore.Get(ctx, storageProvider.DID())
		require.NoError(t, err)
		require.Equal(t, 75, rec.Weight)
		require.NotNil(t, rec.ReplicationWeight)
		require.Equal(t, 30, *rec.ReplicationWeight)
	})
}
