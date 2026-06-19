package handlers_test

import (
	"context"
	"testing"

	"github.com/fil-forge/libforge/attestation/didmailto"
	providercmds "github.com/fil-forge/libforge/commands/provider"
	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/billing"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	customer_store "github.com/fil-forge/sprue/pkg/store/customer/memory"
	subscription_store "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type providerAddDeps struct {
	provisioningSvc *provisioning.Service
	billingSvc      *billing.Service
	customerStore   *customer_store.Store
}

func setupProviderAdd(t *testing.T, providerDID did.DID) *providerAddDeps {
	t.Helper()
	customerStore := customer_store.New()
	provisioningSvc := provisioning.NewService(
		[]did.DID{providerDID},
		consumer_store.New(),
		subscription_store.New(),
	)
	billingSvc := billing.NewService(customerStore)
	return &providerAddDeps{
		provisioningSvc: provisioningSvc,
		billingSvc:      billingSvc,
		customerStore:   customerStore,
	}
}

// invokeProviderAdd builds a /provider/add invocation with the account as the
// subject (matching the handler's expectation), plus a signed response.
func invokeProviderAdd(
	t *testing.T,
	ctx context.Context,
	agent ucan.Issuer,
	uploadService ucan.Issuer,
	account did.DID,
	args *providercmds.AddArguments,
) (execution.Request, *execution.ExecResponse) {
	t.Helper()
	inv, err := providercmds.Add.Invoke(
		agent,
		account,
		args,
		invocation.WithAudience(uploadService.DID()),
	)
	require.NoError(t, err)
	req := execution.NewRequest(ctx, inv)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
	require.NoError(t, err)
	return req, res
}

func TestProviderAddHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("success with payment plan", func(t *testing.T) {
		serviceProvider := testutil.RandomIssuer(t)
		deps := setupProviderAdd(t, serviceProvider.DID())

		account := testutil.Must(didmailto.New("alice@example.com"))(t)
		product := testutil.Must(did.Parse("did:web:free.web3.storage"))(t)
		require.NoError(t, deps.customerStore.Add(ctx, account, nil, product, nil, nil))

		handler := handlers.NewProviderAddHandler(
			config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: false},
			deps.provisioningSvc, deps.billingSvc, logger,
		)

		space := testutil.RandomIssuer(t)
		agent := testutil.RandomIssuer(t)
		req, res := invokeProviderAdd(t, ctx, agent, uploadService, account,
			&providercmds.AddArguments{
				Provider: serviceProvider.DID(),
				Consumer: space.DID(),
			},
		)

		err := handler.Handler(req, res)
		require.NoError(t, err)

		ok, err := providercmds.Add.Unpack(res.Receipt())
		require.NoError(t, err)
		require.NotEmpty(t, ok.ID)
	})

	t.Run("success skipping payment plan check", func(t *testing.T) {
		serviceProvider := testutil.RandomIssuer(t)
		deps := setupProviderAdd(t, serviceProvider.DID())

		// No customer added — but payment plan check is skipped.
		handler := handlers.NewProviderAddHandler(
			config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: true},
			deps.provisioningSvc, deps.billingSvc, logger,
		)

		account := testutil.Must(didmailto.New("alice@example.com"))(t)
		space := testutil.RandomIssuer(t)
		agent := testutil.RandomIssuer(t)
		req, res := invokeProviderAdd(t, ctx, agent, uploadService, account,
			&providercmds.AddArguments{
				Provider: serviceProvider.DID(),
				Consumer: space.DID(),
			},
		)

		err := handler.Handler(req, res)
		require.NoError(t, err)

		ok, err := providercmds.Add.Unpack(res.Receipt())
		require.NoError(t, err)
		require.NotEmpty(t, ok.ID)
	})

	t.Run("invalid account DID", func(t *testing.T) {
		serviceProvider := testutil.RandomIssuer(t)
		deps := setupProviderAdd(t, serviceProvider.DID())

		handler := handlers.NewProviderAddHandler(
			config.DeploymentConfig{},
			deps.provisioningSvc, deps.billingSvc, logger,
		)

		// Subject is a did:key (not a did:mailto), so didmailto.Parse rejects it.
		notAMailto := testutil.RandomIssuer(t)
		space := testutil.RandomIssuer(t)
		agent := testutil.RandomIssuer(t)
		req, res := invokeProviderAdd(t, ctx, agent, uploadService, notAMailto.DID(),
			&providercmds.AddArguments{
				Provider: serviceProvider.DID(),
				Consumer: space.DID(),
			},
		)

		err := handler.Handler(req, res)
		require.NoError(t, err)

		_, err = providercmds.Add.Unpack(res.Receipt())
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		require.Equal(t, providercmds.InvalidAccountErrorName, errModel.Name())
	})

	t.Run("missing payment plan", func(t *testing.T) {
		serviceProvider := testutil.RandomIssuer(t)
		deps := setupProviderAdd(t, serviceProvider.DID())

		// No customer added — payment plan check fails with ErrMissingPaymentPlan.
		handler := handlers.NewProviderAddHandler(
			config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: false},
			deps.provisioningSvc, deps.billingSvc, logger,
		)

		account := testutil.Must(didmailto.New("alice@example.com"))(t)
		space := testutil.RandomIssuer(t)
		agent := testutil.RandomIssuer(t)
		req, res := invokeProviderAdd(t, ctx, agent, uploadService, account,
			&providercmds.AddArguments{
				Provider: serviceProvider.DID(),
				Consumer: space.DID(),
			},
		)

		err := handler.Handler(req, res)
		require.NoError(t, err)

		_, err = providercmds.Add.Unpack(res.Receipt())
		require.ErrorIs(t, err, providercmds.ErrAccountPlanMissing)
	})

	t.Run("provider not allowed", func(t *testing.T) {
		serviceProvider := testutil.RandomIssuer(t)
		deps := setupProviderAdd(t, serviceProvider.DID())

		handler := handlers.NewProviderAddHandler(
			config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: true},
			deps.provisioningSvc, deps.billingSvc, logger,
		)

		// Args reference a different provider than the one allowed in setup.
		otherProvider := testutil.RandomIssuer(t)
		account := testutil.Must(didmailto.New("alice@example.com"))(t)
		space := testutil.RandomIssuer(t)
		agent := testutil.RandomIssuer(t)
		req, res := invokeProviderAdd(t, ctx, agent, uploadService, account,
			&providercmds.AddArguments{
				Provider: otherProvider.DID(),
				Consumer: space.DID(),
			},
		)

		err := handler.Handler(req, res)
		require.NoError(t, err)

		_, err = providercmds.Add.Unpack(res.Receipt())
		require.ErrorIs(t, err, provisioning.ErrProviderNotAllowed)
	})
}
