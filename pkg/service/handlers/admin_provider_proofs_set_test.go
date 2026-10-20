package handlers_test

import (
	"net/url"
	"testing"

	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/commands/admin/provider/proofs"
	"github.com/fil-forge/sprue/pkg/service/handlers"
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

func issueProofsSetInvocation(
	t *testing.T,
	issuer ucan.Issuer,
	audience did.DID,
	args proofs.SetArguments,
) execution.Request {
	t.Helper()

	inv, err := proofs.Set.Invoke(
		issuer,
		audience,
		&args,
		invocation.WithAudience(audience),
	)
	require.NoError(t, err)

	return execution.NewRequest(t.Context(), inv)
}

func TestAdminProviderProofsSetHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService
	endpoint, err := url.Parse("https://piri.example.com")
	require.NoError(t, err)

	// setProofs runs the handler and returns the failure name, or "" on success.
	setProofs := func(t *testing.T, handler func(execution.Request, execution.Response) error, issuer ucan.Issuer, args proofs.SetArguments) string {
		t.Helper()
		req := issueProofsSetInvocation(t, issuer, uploadService.DID(), args)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		require.NoError(t, handler(req, res))

		_, err = proofs.Set.Unpack(res.Receipt())
		if err == nil {
			return ""
		}
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		return errModel.Name()
	}

	t.Run("unauthorized issuer", func(t *testing.T) {
		spStore := storage_provider_store.New()
		handler := handlers.NewAdminProviderProofsSetHandler(identity.Identity{Issuer: uploadService}, spStore, logger)

		storageProvider := testutil.RandomIssuer(t)
		args := proofs.SetArguments{
			Provider: storageProvider.DID(),
			Proofs:   registerProofs(t, storageProvider, uploadService.DID()),
		}

		require.Equal(t, "Unauthorized", setProofs(t, handler.Handler, testutil.RandomIssuer(t), args))
	})

	t.Run("provider not found", func(t *testing.T) {
		spStore := storage_provider_store.New()
		handler := handlers.NewAdminProviderProofsSetHandler(identity.Identity{Issuer: uploadService}, spStore, logger)

		storageProvider := testutil.RandomIssuer(t)
		args := proofs.SetArguments{
			Provider: storageProvider.DID(),
			Proofs:   registerProofs(t, storageProvider, uploadService.DID()),
		}

		require.Equal(t, "ProviderNotFound", setProofs(t, handler.Handler, uploadService, args))
	})

	t.Run("rejects proofs missing a required delegation", func(t *testing.T) {
		spStore := storage_provider_store.New()
		handler := handlers.NewAdminProviderProofsSetHandler(identity.Identity{Issuer: uploadService}, spStore, logger)

		storageProvider := testutil.RandomIssuer(t)
		original := testutil.Must(container.Decode(registerProofs(t, storageProvider, uploadService.DID())))(t)
		require.NoError(t, spStore.Add(ctx, storageProvider.DID(), *endpoint, 10, nil, original))

		dlg, err := delegation.Delegate(storageProvider, uploadService.DID(), storageProvider.DID(), command.MustParse("/blob/allocate"))
		require.NoError(t, err)
		proofBytes, err := container.Encode(container.Raw, container.New(container.WithDelegations(dlg)))
		require.NoError(t, err)

		args := proofs.SetArguments{Provider: storageProvider.DID(), Proofs: proofBytes}
		require.Equal(t, "InvalidProofs", setProofs(t, handler.Handler, uploadService, args))

		// The stored proofs are unchanged.
		rec, err := spStore.Get(ctx, storageProvider.DID())
		require.NoError(t, err)
		require.Equal(t, original, rec.Proofs)
	})

	t.Run("replaces the proofs and leaves the rest of the record", func(t *testing.T) {
		spStore := storage_provider_store.New()
		handler := handlers.NewAdminProviderProofsSetHandler(identity.Identity{Issuer: uploadService}, spStore, logger)

		storageProvider := testutil.RandomIssuer(t)
		replWeight := 5
		original := testutil.Must(container.Decode(registerProofs(t, storageProvider, uploadService.DID())))(t)
		require.NoError(t, spStore.Add(ctx, storageProvider.DID(), *endpoint, 10, &replWeight, original))

		replacement := registerProofs(t, storageProvider, uploadService.DID())
		args := proofs.SetArguments{Provider: storageProvider.DID(), Proofs: replacement}
		require.Empty(t, setProofs(t, handler.Handler, uploadService, args))

		rec, err := spStore.Get(ctx, storageProvider.DID())
		require.NoError(t, err)
		require.Equal(t, *endpoint, rec.Endpoint)
		require.Equal(t, 10, rec.Weight)
		require.Equal(t, 5, *rec.ReplicationWeight)
		gotBytes, err := container.Encode(container.Raw, rec.Proofs)
		require.NoError(t, err)
		require.Equal(t, replacement, gotBytes)
	})
}
