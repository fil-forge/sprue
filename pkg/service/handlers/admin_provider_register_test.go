package handlers_test

import (
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/capabilities/admin/provider"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/result"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// issueRegisterInvocation creates an admin/provider/register invocation request
func issueRegisterInvocation(
	t *testing.T,
	issuer ucan.Signer,
	audience ucan.Principal,
	args provider.RegisterArguments,
) execution.Request {
	t.Helper()

	inv, err := provider.Register.Invoke(
		issuer,
		audience,
		&args,
		invocation.WithAudience(audience),
	)
	require.NoError(t, err)

	return execution.NewRequest(t.Context(), inv)
}

func TestAdminProviderRegisterHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("unauthorized issuer", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderRegisterHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)
		unauthorizedIssuer := testutil.RandomSigner(t)

		args := provider.RegisterArguments{
			Provider: storageProvider.DID(),
			Endpoint: "https://piri.example.com",
		}

		// Issuer is neither the service nor the provider
		req := issueRegisterInvocation(t, unauthorizedIssuer, uploadService, args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, fail := result.Unwrap(res.Receipt().Out())
		require.NotNil(t, fail)

		model := edm.ErrorModel{}
		err = datamodel.Rebind(datamodel.NewAny(fail), &model)
		require.NoError(t, err)
		require.Equal(t, "Unauthorized", model.Name())
	})

	t.Run("provider already registered", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderRegisterHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)

		args := provider.RegisterArguments{
			Provider: storageProvider.DID(),
			Endpoint: "https://piri.example.com",
		}

		// First registration by service identity (authorized)
		req := issueRegisterInvocation(t, uploadService, uploadService, args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		o, x := result.Unwrap(res.Receipt().Out())
		require.Nil(t, x)
		require.NotNil(t, o)

		// Second registration should fail
		req2 := issueRegisterInvocation(t, uploadService, uploadService, args)
		res2, err := execution.NewResponse(req2.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req2, res2)
		require.NoError(t, err)

		_, x2 := result.Unwrap(res2.Receipt().Out())
		require.NotNil(t, x2)

		model := edm.ErrorModel{}
		err = datamodel.Rebind(datamodel.NewAny(x2), &model)
		require.NoError(t, err)
		require.Equal(t, "ProviderAlreadyRegistered", model.Name())
	})

	t.Run("service identity can register", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderRegisterHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)

		args := provider.RegisterArguments{
			Provider: storageProvider.DID(),
			Endpoint: "https://piri.example.com",
		}

		req := issueRegisterInvocation(t, uploadService, uploadService, args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		o, x := result.Unwrap(res.Receipt().Out())
		require.Nil(t, x)
		require.NotNil(t, o)

		// Verify provider was stored
		rec, err := spStore.Get(ctx, storageProvider.DID())
		require.NoError(t, err)
		require.Equal(t, "https://piri.example.com", rec.Endpoint.String())
	})
}
