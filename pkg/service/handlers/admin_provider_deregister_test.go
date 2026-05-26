package handlers_test

import (
	"net/url"
	"testing"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/commands/admin/provider"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func issueDeregisterInvocation(
	t *testing.T,
	issuer ucan.Signer,
	audience did.DID,
	args provider.DeregisterArguments,
) execution.Request {
	t.Helper()

	inv, err := provider.Deregister.Invoke(
		issuer,
		audience,
		&args,
		invocation.WithAudience(audience),
	)
	require.NoError(t, err)

	return execution.NewRequest(t.Context(), inv)
}

func TestAdminProviderDeregisterHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("unauthorized issuer", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderDeregisterHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)
		unauthorizedIssuer := testutil.RandomSigner(t)

		// Pre-populate the store so we can verify the record is NOT removed.
		endpoint, err := url.Parse("https://piri.example.com")
		require.NoError(t, err)
		err = spStore.Put(ctx, storageProvider.DID(), *endpoint, 0, nil)
		require.NoError(t, err)

		args := provider.DeregisterArguments{
			Provider: storageProvider.DID(),
		}

		req := issueDeregisterInvocation(t, unauthorizedIssuer, uploadService.DID(), args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = provider.Deregister.Unpack(res.Receipt())
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		require.Equal(t, "Unauthorized", errModel.Name())

		// Record should still be present.
		_, err = spStore.Get(ctx, storageProvider.DID())
		require.NoError(t, err)
	})

	t.Run("service identity can deregister", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderDeregisterHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)

		endpoint, err := url.Parse("https://piri.example.com")
		require.NoError(t, err)
		err = spStore.Put(ctx, storageProvider.DID(), *endpoint, 0, nil)
		require.NoError(t, err)

		args := provider.DeregisterArguments{
			Provider: storageProvider.DID(),
		}

		req := issueDeregisterInvocation(t, uploadService, uploadService.DID(), args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)
		_, err = blobcmds.Allocate.Unpack(res.Receipt())
		require.NoError(t, err)

		_, err = spStore.Get(ctx, storageProvider.DID())
		require.ErrorIs(t, err, storageprovider.ErrStorageProviderNotFound)
	})

	t.Run("provider not found", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderDeregisterHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)

		args := provider.DeregisterArguments{
			Provider: storageProvider.DID(),
		}

		req := issueDeregisterInvocation(t, uploadService, uploadService.DID(), args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = provider.Deregister.Unpack(res.Receipt())
		require.ErrorIs(t, err, storageprovider.ErrStorageProviderNotFound)
	})
}
