package handlers_test

import (
	"net/url"
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

func issueListInvocation(
	t *testing.T,
	issuer ucan.Signer,
	audience ucan.Principal,
) execution.Request {
	t.Helper()

	args := provider.ListArguments{}
	inv, err := provider.List.Invoke(
		issuer,
		audience,
		&args,
		invocation.WithAudience(audience),
	)
	require.NoError(t, err)

	return execution.NewRequest(t.Context(), inv)
}

func TestAdminProviderListHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("unauthorized issuer", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderListHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		unauthorizedIssuer := testutil.RandomSigner(t)

		req := issueListInvocation(t, unauthorizedIssuer, uploadService)
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

	t.Run("empty list", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderListHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		req := issueListInvocation(t, uploadService, uploadService)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res.Receipt().Out())
		require.Nil(t, fail)
		require.NotNil(t, ok)

		listOK := provider.ListOK{}
		err = datamodel.Rebind(datamodel.NewAny(ok), &listOK)
		require.NoError(t, err)
		require.Empty(t, listOK.Providers)
	})

	t.Run("returns registered providers", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.NewAdminProviderListHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		sp1 := testutil.RandomSigner(t)
		sp2 := testutil.RandomSigner(t)

		endpoint1, err := url.Parse("https://piri-1.example.com")
		require.NoError(t, err)
		endpoint2, err := url.Parse("https://piri-2.example.com")
		require.NoError(t, err)

		repWeight := 50
		require.NoError(t, spStore.Put(ctx, sp1.DID(), *endpoint1, 100, &repWeight))
		require.NoError(t, spStore.Put(ctx, sp2.DID(), *endpoint2, 200, nil))

		req := issueListInvocation(t, uploadService, uploadService)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res.Receipt().Out())
		require.Nil(t, fail)
		require.NotNil(t, ok)

		listOK := provider.ListOK{}
		err = datamodel.Rebind(datamodel.NewAny(ok), &listOK)
		require.NoError(t, err)
		require.Len(t, listOK.Providers, 2)

		byDID := map[string]provider.Provider{}
		for _, p := range listOK.Providers {
			byDID[p.Provider.String()] = p
		}

		got1, ok1 := byDID[sp1.DID().String()]
		require.True(t, ok1)
		require.Equal(t, "https://piri-1.example.com", got1.Endpoint)
		require.Equal(t, int64(100), got1.Weight)
		require.Equal(t, int64(50), got1.ReplicationWeight)

		got2, ok2 := byDID[sp2.DID().String()]
		require.True(t, ok2)
		require.Equal(t, "https://piri-2.example.com", got2.Endpoint)
		require.Equal(t, int64(200), got2.Weight)
		// When ReplicationWeight is nil in the store, the handler defaults it to Weight.
		require.Equal(t, int64(200), got2.ReplicationWeight)
	})
}
