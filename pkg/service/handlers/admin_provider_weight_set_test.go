package handlers_test

import (
	"net/url"
	"testing"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/commands/admin/provider/weight"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func issueWeightSetInvocation(
	t *testing.T,
	issuer ucan.Signer,
	audience did.DID,
	args weight.SetArguments,
) execution.Request {
	t.Helper()

	inv, err := weight.Set.Invoke(
		issuer,
		audience,
		&args,
		invocation.WithAudience(audience),
	)
	require.NoError(t, err)

	return execution.NewRequest(t.Context(), inv)
}

func TestAdminProviderWeightSetHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("unauthorized issuer", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderWeightSetHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)
		unauthorizedIssuer := testutil.RandomSigner(t)

		args := weight.SetArguments{
			Provider:          storageProvider.DID(),
			Weight:            50,
			ReplicationWeight: 25,
		}

		req := issueWeightSetInvocation(t, unauthorizedIssuer, uploadService.DID(), args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = weight.Set.Unpack(res.Receipt())
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		require.Equal(t, "Unauthorized", errModel.Name())
	})

	t.Run("provider not found", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderWeightSetHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)

		args := weight.SetArguments{
			Provider:          storageProvider.DID(),
			Weight:            50,
			ReplicationWeight: 25,
		}

		req := issueWeightSetInvocation(t, uploadService, uploadService.DID(), args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = weight.Set.Unpack(res.Receipt())
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		require.Equal(t, "Failed to get existing provider", errModel.Name())
	})

	t.Run("success updates weights", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderWeightSetHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)
		endpoint, err := url.Parse("https://piri.example.com")
		require.NoError(t, err)

		// Pre-register the provider with initial weights.
		initialReplWeight := 0
		err = spStore.Put(ctx, storageProvider.DID(), *endpoint, 0, &initialReplWeight, container.New())
		require.NoError(t, err)

		args := weight.SetArguments{
			Provider:          storageProvider.DID(),
			Weight:            75,
			ReplicationWeight: 30,
		}

		req := issueWeightSetInvocation(t, uploadService, uploadService.DID(), args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = blobcmds.Allocate.Unpack(res.Receipt())
		require.NoError(t, err)

		// Verify weights were updated.
		rec, err := spStore.Get(ctx, storageProvider.DID())
		require.NoError(t, err)
		require.Equal(t, 75, rec.Weight)
		require.NotNil(t, rec.ReplicationWeight)
		require.Equal(t, 30, *rec.ReplicationWeight)
	})
}
