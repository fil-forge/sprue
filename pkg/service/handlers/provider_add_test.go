package handlers

import (
	"context"
	"testing"

	"github.com/fil-forge/go-libstoracha/capabilities/provider"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/ucan"

	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/pkg/billing"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/provisioning"
	consumermemory "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	customermemory "github.com/fil-forge/sprue/pkg/store/customer/memory"
	subscriptionmemory "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupProviderAdd(t *testing.T, addCustomer bool) (*identity.Identity, did.DID, did.DID, *provisioning.Service, *billing.Service) {
	t.Helper()

	serviceID := newTestIdentity(t)
	providerDID := serviceID.Signer.DID()
	account := mustMailtoDID(t, "alice@example.com")
	product, err := did.Parse("did:web:free.web3.storage")
	require.NoError(t, err)

	customerStore := customermemory.New()
	if addCustomer {
		err = customerStore.Add(context.Background(), account, nil, product, nil, nil)
		require.NoError(t, err)
	}

	provisioningSvc := provisioning.NewService(
		[]did.DID{providerDID},
		consumermemory.New(),
		subscriptionmemory.New(),
	)

	billingSvc := billing.NewService(customerStore)

	return serviceID, providerDID, account, provisioningSvc, billingSvc
}

func TestProviderAddHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("success with payment plan", func(t *testing.T) {
		deployCfg := config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: false}
		serviceID, providerDID, account, provisioningSvc, billingSvc := setupProviderAdd(t, true)
		handler := ProviderAddHandler(deployCfg, provisioningSvc, billingSvc, logger)

		space := newTestIdentity(t)

		cap := ucan.NewCapability(
			provider.AddAbility,
			account.String(),
			provider.AddCaveats{
				Provider: providerDID.String(),
				Consumer: space.DID(),
			},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, serviceID.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.NotEmpty(t, ok.Id)
	})

	t.Run("success skipping payment plan check", func(t *testing.T) {
		deployCfg := config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: true}
		// No customer added — but payment plan check is skipped
		serviceID, providerDID, account, provisioningSvc, billingSvc := setupProviderAdd(t, false)
		handler := ProviderAddHandler(deployCfg, provisioningSvc, billingSvc, logger)

		space := newTestIdentity(t)

		cap := ucan.NewCapability(
			provider.AddAbility,
			account.String(),
			provider.AddCaveats{
				Provider: providerDID.String(),
				Consumer: space.DID(),
			},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, serviceID.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.NotEmpty(t, ok.Id)
	})

	t.Run("invalid account DID", func(t *testing.T) {
		deployCfg := config.DeploymentConfig{}
		serviceID, providerDID, _, provisioningSvc, billingSvc := setupProviderAdd(t, false)
		handler := ProviderAddHandler(deployCfg, provisioningSvc, billingSvc, logger)

		space := newTestIdentity(t)

		cap := ucan.NewCapability(
			provider.AddAbility,
			"not-a-mailto-did",
			provider.AddCaveats{
				Provider: providerDID.String(),
				Consumer: space.DID(),
			},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, serviceID.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("invalid provider DID", func(t *testing.T) {
		deployCfg := config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: true}
		serviceID, _, account, provisioningSvc, billingSvc := setupProviderAdd(t, false)
		handler := ProviderAddHandler(deployCfg, provisioningSvc, billingSvc, logger)

		space := newTestIdentity(t)

		cap := ucan.NewCapability(
			provider.AddAbility,
			account.String(),
			provider.AddCaveats{
				Provider: "bad-provider",
				Consumer: space.DID(),
			},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, serviceID.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("invalid space DID", func(t *testing.T) {
		deployCfg := config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: true}
		serviceID, providerDID, account, provisioningSvc, billingSvc := setupProviderAdd(t, false)
		handler := ProviderAddHandler(deployCfg, provisioningSvc, billingSvc, logger)

		cap := ucan.NewCapability(
			provider.AddAbility,
			account.String(),
			provider.AddCaveats{
				Provider: providerDID.String(),
				Consumer: "bad-space",
			},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, serviceID.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("missing payment plan", func(t *testing.T) {
		deployCfg := config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: false}
		// No customer added — payment plan check will fail
		serviceID, providerDID, account, provisioningSvc, billingSvc := setupProviderAdd(t, false)
		handler := ProviderAddHandler(deployCfg, provisioningSvc, billingSvc, logger)

		space := newTestIdentity(t)

		cap := ucan.NewCapability(
			provider.AddAbility,
			account.String(),
			provider.AddCaveats{
				Provider: providerDID.String(),
				Consumer: space.DID(),
			},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, serviceID.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("provider not allowed", func(t *testing.T) {
		deployCfg := config.DeploymentConfig{AllowProvisionWithoutPaymentPlan: true}
		serviceID, _, account, provisioningSvc, billingSvc := setupProviderAdd(t, false)
		handler := ProviderAddHandler(deployCfg, provisioningSvc, billingSvc, logger)

		space := newTestIdentity(t)
		otherProvider := newTestIdentity(t)

		cap := ucan.NewCapability(
			provider.AddAbility,
			account.String(),
			provider.AddCaveats{
				Provider: otherProvider.DID(),
				Consumer: space.DID(),
			},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, serviceID.Signer, cap)
		require.NoError(t, err)

		_, _, err = handler(context.Background(), cap, inv, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "provisioning service")
	})
}
