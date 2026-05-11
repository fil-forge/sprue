package handlers_test

import (
	"context"
	"testing"

	providercaps "github.com/fil-forge/libforge/capabilities/provider"
	"github.com/fil-forge/libforge/didmailto"
	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/billing"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	customer_store "github.com/fil-forge/sprue/pkg/store/customer/memory"
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
	agent principal.Signer,
	uploadService principal.Signer,
	account ucan.Principal,
	args *providercaps.AddArguments,
) (execution.Request, *execution.ExecResponse) {
	t.Helper()
	inv, err := providercaps.Add.Invoke(
		agent,
		account,
		args,
		invocation.WithAudience(uploadService),
	)
	require.NoError(t, err)
	req := execution.NewRequest(ctx, inv)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
	require.NoError(t, err)
	return req, res
}

func TestProviderAddHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("success with payment plan", func(t *testing.T) {
		serviceProvider := testutil.RandomSigner(t)
		deps := setupProviderAdd(t, serviceProvider.DID())

		account := testutil.Must(didmailto.New("alice@example.com"))(t)
		product := testutil.Must(did.Parse("did:web:free.web3.storage"))(t)
		require.NoError(t, deps.customerStore.Add(ctx, account, nil, product, nil, nil))

		handler := handlers.NewProviderAddHandler(
			config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: false},
			deps.provisioningSvc, deps.billingSvc, logger,
		)

		space := testutil.RandomSigner(t)
		agent := testutil.RandomSigner(t)
		req, res := invokeProviderAdd(t, ctx, agent, uploadService, account,
			&providercaps.AddArguments{
				Provider: serviceProvider.DID(),
				Consumer: space.DID(),
			},
		)

		err := handler.Handler(req, res)
		require.NoError(t, err)

		o, fail := result.Unwrap(res.Receipt().Out())
		require.Nil(t, fail)
		require.NotNil(t, o)

		ok := providercaps.AddOK{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(o), &ok))
		require.NotEmpty(t, ok.ID)
	})

	t.Run("success skipping payment plan check", func(t *testing.T) {
		serviceProvider := testutil.RandomSigner(t)
		deps := setupProviderAdd(t, serviceProvider.DID())

		// No customer added — but payment plan check is skipped.
		handler := handlers.NewProviderAddHandler(
			config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: true},
			deps.provisioningSvc, deps.billingSvc, logger,
		)

		account := testutil.Must(didmailto.New("alice@example.com"))(t)
		space := testutil.RandomSigner(t)
		agent := testutil.RandomSigner(t)
		req, res := invokeProviderAdd(t, ctx, agent, uploadService, account,
			&providercaps.AddArguments{
				Provider: serviceProvider.DID(),
				Consumer: space.DID(),
			},
		)

		err := handler.Handler(req, res)
		require.NoError(t, err)

		o, fail := result.Unwrap(res.Receipt().Out())
		require.Nil(t, fail)
		ok := providercaps.AddOK{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(o), &ok))
		require.NotEmpty(t, ok.ID)
	})

	t.Run("invalid account DID", func(t *testing.T) {
		serviceProvider := testutil.RandomSigner(t)
		deps := setupProviderAdd(t, serviceProvider.DID())

		handler := handlers.NewProviderAddHandler(
			config.DeploymentConfig{},
			deps.provisioningSvc, deps.billingSvc, logger,
		)

		// Subject is a did:key (not a did:mailto), so didmailto.Parse rejects it.
		notAMailto := testutil.RandomSigner(t)
		space := testutil.RandomSigner(t)
		agent := testutil.RandomSigner(t)
		req, res := invokeProviderAdd(t, ctx, agent, uploadService, notAMailto,
			&providercaps.AddArguments{
				Provider: serviceProvider.DID(),
				Consumer: space.DID(),
			},
		)

		err := handler.Handler(req, res)
		require.NoError(t, err)

		_, fail := result.Unwrap(res.Receipt().Out())
		require.NotNil(t, fail)

		model := edm.ErrorModel{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(fail), &model))
		require.Equal(t, providercaps.InvalidAccountErrorName, model.Name())
	})

	t.Run("missing payment plan", func(t *testing.T) {
		serviceProvider := testutil.RandomSigner(t)
		deps := setupProviderAdd(t, serviceProvider.DID())

		// No customer added — payment plan check fails with ErrMissingPaymentPlan.
		handler := handlers.NewProviderAddHandler(
			config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: false},
			deps.provisioningSvc, deps.billingSvc, logger,
		)

		account := testutil.Must(didmailto.New("alice@example.com"))(t)
		space := testutil.RandomSigner(t)
		agent := testutil.RandomSigner(t)
		req, res := invokeProviderAdd(t, ctx, agent, uploadService, account,
			&providercaps.AddArguments{
				Provider: serviceProvider.DID(),
				Consumer: space.DID(),
			},
		)

		err := handler.Handler(req, res)
		require.NoError(t, err)

		_, fail := result.Unwrap(res.Receipt().Out())
		require.NotNil(t, fail)

		model := edm.ErrorModel{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(fail), &model))
		require.Equal(t, providercaps.AccountPlanMissingErrorName, model.Name())
	})

	t.Run("provider not allowed", func(t *testing.T) {
		serviceProvider := testutil.RandomSigner(t)
		deps := setupProviderAdd(t, serviceProvider.DID())

		handler := handlers.NewProviderAddHandler(
			config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: true},
			deps.provisioningSvc, deps.billingSvc, logger,
		)

		// Args reference a different provider than the one allowed in setup.
		otherProvider := testutil.RandomSigner(t)
		account := testutil.Must(didmailto.New("alice@example.com"))(t)
		space := testutil.RandomSigner(t)
		agent := testutil.RandomSigner(t)
		req, res := invokeProviderAdd(t, ctx, agent, uploadService, account,
			&providercaps.AddArguments{
				Provider: otherProvider.DID(),
				Consumer: space.DID(),
			},
		)

		err := handler.Handler(req, res)
		require.NoError(t, err)

		_, fail := result.Unwrap(res.Receipt().Out())
		require.NotNil(t, fail)

		model := edm.ErrorModel{}
		require.NoError(t, datamodel.Rebind(datamodel.NewAny(fail), &model))
		require.Equal(t, provisioning.ProviderNotAllowedErrorName, model.Name())
	})
}
