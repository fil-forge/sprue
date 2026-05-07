package handlers

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/fil-forge/go-libstoracha/capabilities/provider"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"

	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/pkg/billing"
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/fil-forge/sprue/pkg/lib/didmailto"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/provisioning"
)

const (
	InvalidAccountErrorName     = "InvalidAccount"
	InvalidProviderErrorName    = "InvalidProvider"
	AccountPlanMissingErrorName = "AccountPlanMissing"
)

// WithProviderAddMethod registers the provider/add handler.
// This handler provisions a space to an account.
func WithProviderAddMethod(deploymentCfg config.DeploymentConfig, provisioningSvc *provisioning.Service, billingSvc *billing.Service, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		provider.AddAbility,
		server.Provide(
			provider.Add,
			ProviderAddHandler(deploymentCfg, provisioningSvc, billingSvc, logger),
		),
	)
}

func ProviderAddHandler(deploymentCfg config.DeploymentConfig, provisioningSvc *provisioning.Service, billingSvc *billing.Service, logger *zap.Logger) server.HandlerFunc[provider.AddCaveats, provider.AddOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", provider.AddAbility))
	return func(ctx context.Context,
		cap ucan.Capability[provider.AddCaveats],
		inv invocation.Invocation,
		iCtx server.InvocationContext,
	) (result.Result[provider.AddOk, failure.IPLDBuilderFailure], fx.Effects, error) {
		account, err := didmailto.Parse(cap.With())
		if err != nil {
			log.Warn("invalid account", zap.String("account", cap.With()))
			return result.Error[provider.AddOk, failure.IPLDBuilderFailure](
				errors.New(InvalidAccountErrorName, "invalid account DID: %v", err),
			), nil, nil
		}
		log := log.With(zap.Stringer("account", account))

		serviceProvider, err := did.Parse(cap.Nb().Provider)
		if err != nil {
			log.Warn("invalid provider", zap.String("provider", cap.Nb().Provider))
			return result.Error[provider.AddOk, failure.IPLDBuilderFailure](
				errors.New(InvalidProviderErrorName, "invalid provider DID: %v", err),
			), nil, nil
		}
		log = log.With(zap.Stringer("provider", serviceProvider))

		space, err := did.Parse(cap.Nb().Consumer)
		if err != nil {
			log.Warn("invalid space", zap.String("space", cap.Nb().Consumer))
			return result.Error[provider.AddOk, failure.IPLDBuilderFailure](
				errors.New(InvalidProviderErrorName, "invalid space DID: %v", err),
			), nil, nil
		}
		log = log.With(zap.Stringer("space", space))
		log.Debug("provisioning service for account", zap.Stringer("account", account))

		if !deploymentCfg.AllowProvisionWithoutPaymentPlan {
			// Check if the account has an active payment plan
			// If not, return an error
			plan, err := billingSvc.PaymentPlan(ctx, account)
			if err != nil {
				if errors.Is(err, billing.ErrMissingPaymentPlan) {
					log.Warn("account does not have an active payment plan")
					return result.Error[provider.AddOk, failure.IPLDBuilderFailure](
						errors.New(AccountPlanMissingErrorName, "account does not have an active payment plan"),
					), nil, nil
				}
				return nil, nil, fmt.Errorf("checking payment plan: %w", err)
			}
			log = log.With(zap.Stringer("plan", plan))
		}

		cause, err := ipldutil.ToCID(inv.Link())
		if err != nil {
			return nil, nil, err
		}

		sub, err := provisioningSvc.Provision(ctx, account, space, serviceProvider, cause)
		if err != nil {
			log.Error("failed to provision service", zap.Error(err))
			return nil, nil, fmt.Errorf("provisioning service: %w", err)
		}

		log.Debug("service provisioned successfully", zap.String("subscription", sub))

		return result.Ok[provider.AddOk, failure.IPLDBuilderFailure](provider.AddOk{
			Id: sub,
		}), nil, nil
	}
}
