package handlers

import (
	"fmt"

	providercaps "github.com/fil-forge/libforge/capabilities/provider"
	"github.com/fil-forge/libforge/didmailto"
	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/pkg/billing"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"go.uber.org/zap"
)

func NewProviderAddHandler(deploymentCfg config.DeploymentConfig, provisioningSvc *provisioning.Service, billingSvc *billing.Service, logger *zap.Logger) Handler {
	log := logger.With(zap.String("handler", providercaps.AddCommand))
	return Handler{
		Capability: providercaps.Add,
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*providercaps.AddArguments],
			res *bindexec.Response[*providercaps.AddOK],
		) error {
			args := req.Task().Arguments()
			account, err := didmailto.Parse(req.Invocation().Subject().DID().String())
			if err != nil {
				log.Warn("invalid account", zap.Stringer("account", req.Invocation().Subject().DID()))
				return res.SetFailure(errors.New(providercaps.InvalidAccountErrorName, "invalid account DID: %v", err))
			}
			serviceProvider := args.Provider
			space := args.Consumer
			cause := req.Invocation().Task().Link()

			log = log.With(
				zap.Stringer("account", account),
				zap.Stringer("provider", serviceProvider),
				zap.Stringer("space", space),
			)
			log.Debug("provisioning service for account")

			if !deploymentCfg.AllowProvisionWithoutPaymentPlan {
				// Check if the account has an active payment plan
				// If not, return an error
				plan, err := billingSvc.PaymentPlan(req.Context(), account)
				if err != nil {
					if errors.Is(err, billing.ErrMissingPaymentPlan) {
						log.Warn("account does not have an active payment plan")
						return res.SetFailure(providercaps.ErrAccountPlanMissing)
					}
					log.Error("failed to check payment plan", zap.Error(err))
					return fmt.Errorf("checking payment plan: %w", err)
				}
				log = log.With(zap.Stringer("plan", plan))
			}

			sub, err := provisioningSvc.Provision(req.Context(), account, space, serviceProvider, cause)
			if err != nil {
				if errors.Is(err, provisioning.ErrProviderNotAllowed) {
					log.Warn("provider is not allowed for this space")
					return res.SetFailure(err)
				}
				if errors.Is(err, consumer.ErrConsumerExists) {
					log.Warn("consumer already exists for this space")
					return res.SetFailure(err)
				}
				log.Error("failed to provision service", zap.Error(err))
				return fmt.Errorf("provisioning service: %w", err)
			}

			log.Debug("service provisioned successfully", zap.String("subscription", sub))
			return res.SetSuccess(&providercaps.AddOK{ID: sub})
		}),
	}
}
