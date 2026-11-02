package handlers_test

import (
	"testing"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	replicacmds "github.com/fil-forge/libforge/commands/blob/replica"
	pdpcmds "github.com/fil-forge/libforge/commands/pdp"
	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/commands/admin/provider"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/command"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/delegation"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// requiredProofCommands are the capabilities the register handler expects the
// provider to delegate to the upload service.
var requiredProofCommands = []ucan.Command{
	blobcmds.Allocate.Command,
	blobcmds.Accept.Command,
	replicacmds.Allocate.Command,
	pdpcmds.Info.Command,
}

// registerProofs returns an encoded UCAN container delegating the required
// allocation capabilities from the provider to the upload service, as expected
// by the register handler.
func registerProofs(t *testing.T, providerIssuer ucan.Issuer, audience did.DID) []byte {
	t.Helper()
	dlgs := make([]ucan.Delegation, 0, len(requiredProofCommands))
	for _, cmd := range requiredProofCommands {
		dlg, err := delegation.Delegate(providerIssuer, audience, providerIssuer.DID(), cmd)
		require.NoError(t, err)
		dlgs = append(dlgs, dlg)
	}
	proofBytes, err := container.Encode(container.Raw, container.New(container.WithDelegations(dlgs...)))
	require.NoError(t, err)
	return proofBytes
}

// issueRegisterInvocation creates an admin/provider/register invocation request
func issueRegisterInvocation(
	t *testing.T,
	issuer ucan.Issuer,
	audience did.DID,
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
			identity.Identity{Issuer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomIssuer(t)
		unauthorizedIssuer := testutil.RandomIssuer(t)

		args := provider.RegisterArguments{
			Provider: storageProvider.DID(),
			Endpoint: "https://piri.example.com",
		}

		// Issuer is neither the service nor the provider
		req := issueRegisterInvocation(t, unauthorizedIssuer, uploadService.DID(), args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = provider.Register.Unpack(res.Receipt())
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		require.Equal(t, "Unauthorized", errModel.Name())
	})

	t.Run("provider already registered", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderRegisterHandler(
			identity.Identity{Issuer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomIssuer(t)

		args := provider.RegisterArguments{
			Provider: storageProvider.DID(),
			Endpoint: "https://piri.example.com",
			Proofs:   registerProofs(t, storageProvider, uploadService.DID()),
		}

		// First registration by service identity (authorized)
		req := issueRegisterInvocation(t, uploadService, uploadService.DID(), args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)
		_, err = blobcmds.Allocate.Unpack(res.Receipt())
		require.NoError(t, err)

		// Second registration should fail
		req2 := issueRegisterInvocation(t, uploadService, uploadService.DID(), args)
		res2, err := execution.NewResponse(req2.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req2, res2)
		require.NoError(t, err)
		require.True(t, res2.Receipt().Out().IsErr())

		_, err = provider.Register.Unpack(res2.Receipt())
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		require.Equal(t, "ProviderAlreadyRegistered", errModel.Name())
	})

	t.Run("rejects proofs missing a required delegation", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderRegisterHandler(
			identity.Identity{Issuer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomIssuer(t)

		// Delegate only /blob/allocate, omitting /blob/accept and
		// /blob/replica/allocate.
		dlg, err := delegation.Delegate(storageProvider, uploadService.DID(), storageProvider.DID(), command.MustParse("/blob/allocate"))
		require.NoError(t, err)
		proofBytes, err := container.Encode(container.Raw, container.New(container.WithDelegations(dlg)))
		require.NoError(t, err)

		args := provider.RegisterArguments{
			Provider: storageProvider.DID(),
			Endpoint: "https://piri.example.com",
			Proofs:   proofBytes,
		}

		req := issueRegisterInvocation(t, uploadService, uploadService.DID(), args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = provider.Register.Unpack(res.Receipt())
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		require.Equal(t, "InvalidProofs", errModel.Name())

		// Provider must not have been stored.
		_, err = spStore.Get(ctx, storageProvider.DID())
		require.ErrorIs(t, err, storageprovider.ErrStorageProviderNotFound)
	})

	t.Run("service identity can register", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderRegisterHandler(
			identity.Identity{Issuer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomIssuer(t)

		args := provider.RegisterArguments{
			Provider: storageProvider.DID(),
			Endpoint: "https://piri.example.com",
			Proofs:   registerProofs(t, storageProvider, uploadService.DID()),
		}

		req := issueRegisterInvocation(t, uploadService, uploadService.DID(), args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)
		_, err = blobcmds.Allocate.Unpack(res.Receipt())
		require.NoError(t, err)

		// Verify provider was stored
		rec, err := spStore.Get(ctx, storageProvider.DID())
		require.NoError(t, err)
		require.Equal(t, "https://piri.example.com", rec.Endpoint.String())
	})
}
