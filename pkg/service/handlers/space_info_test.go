package handlers_test

import (
	"context"
	"testing"

	spacecaps "github.com/fil-forge/libforge/capabilities/space"
	"github.com/fil-forge/libforge/didmailto"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	subscription_store "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	"github.com/fil-forge/ucantone/did"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/principal"
	"github.com/fil-forge/ucantone/result"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// invokeSpaceInfo builds a /space/info invocation against the given space and
// returns the request + signed response.
func invokeSpaceInfo(
	t *testing.T,
	ctx context.Context,
	agent principal.Signer,
	uploadService principal.Signer,
	space ucan.Principal,
) (execution.Request, *execution.ExecResponse) {
	t.Helper()
	inv, err := spacecaps.Info.Invoke(
		agent,
		space,
		&spacecaps.InfoArguments{},
		invocation.WithAudience(uploadService),
	)
	require.NoError(t, err)
	req := execution.NewRequest(ctx, inv)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
	require.NoError(t, err)
	return req, res
}

func TestSpaceInfoHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("returns providers for a provisioned space", func(t *testing.T) {
		consumerStore := consumer_store.New()
		subscriptionStore := subscription_store.New()
		provisioningSvc := provisioning.NewService(
			[]did.DID{uploadService.DID()},
			consumerStore,
			subscriptionStore,
		)

		handler := handlers.NewSpaceInfoHandler(provisioningSvc, logger)

		space := testutil.RandomSigner(t)
		account := testutil.Must(didmailto.New("alice@example.com"))(t)

		_, err := provisioningSvc.Provision(ctx, account, space.DID(), uploadService.DID(), testutil.RandomCID(t))
		require.NoError(t, err)

		req, res := invokeSpaceInfo(t, ctx, testutil.Alice, uploadService, space)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		o, fail := result.Unwrap(res.Receipt().Out())
		require.Nil(t, fail)
		require.NotNil(t, o)

		ok := spacecaps.InfoOK{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(o), &ok))
		require.Len(t, ok.Providers, 1)
		require.Equal(t, uploadService.DID(), ok.Providers[0])
	})

	t.Run("returns empty providers for unprovisioned did:key space", func(t *testing.T) {
		provisioningSvc := provisioning.NewService(
			[]did.DID{uploadService.DID()},
			consumer_store.New(),
			subscription_store.New(),
		)

		handler := handlers.NewSpaceInfoHandler(provisioningSvc, logger)

		space := testutil.RandomSigner(t)

		req, res := invokeSpaceInfo(t, ctx, testutil.Alice, uploadService, space)

		err := handler.Handler(req, res)
		require.NoError(t, err)

		o, fail := result.Unwrap(res.Receipt().Out())
		require.Nil(t, fail)

		ok := spacecaps.InfoOK{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(o), &ok))
		require.Empty(t, ok.Providers)
	})

	t.Run("returns UnknownSpace for non did:key space", func(t *testing.T) {
		provisioningSvc := provisioning.NewService(
			[]did.DID{},
			consumer_store.New(),
			subscription_store.New(),
		)

		handler := handlers.NewSpaceInfoHandler(provisioningSvc, logger)

		// did:web subject — handler rejects since only did:key spaces are
		// supported.
		webDID := testutil.Must(did.Parse("did:web:example.com"))(t)
		req, res := invokeSpaceInfo(t, ctx, testutil.Alice, uploadService, webDID)

		err := handler.Handler(req, res)
		require.NoError(t, err)

		_, fail := result.Unwrap(res.Receipt().Out())
		require.NotNil(t, fail)

		model := edm.ErrorModel{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(fail), &model))
		require.Equal(t, spacecaps.UnknownSpaceErrorName, model.Name())
	})
}
