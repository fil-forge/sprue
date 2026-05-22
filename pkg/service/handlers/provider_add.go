package handlers

import (
	"fmt"

	providercmds "github.com/fil-forge/libforge/commands/provider"
	"github.com/fil-forge/libforge/didmailto"
	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/pkg/billing"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"go.uber.org/zap"
)

func NewProviderAddHandler(deploymentCfg config.DeploymentConfig, provisioningSvc *provisioning.Service, billingSvc *billing.Service, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", providercmds.Add))
	return server.NewRoute(
		providercmds.Add,
		func(req *binding.Request[*providercmds.AddArguments], res *binding.Response[*providercmds.AddOK]) error {
			args := req.Task().Arguments()
			account, err := didmailto.Parse(req.Invocation().Subject().String())
			if err != nil {
				log.Warn("invalid account", zap.Stringer("account", req.Invocation().Subject()))
				return res.SetFailure(errors.New(providercmds.InvalidAccountErrorName, "invalid account DID: %v", err))
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
						return res.SetFailure(providercmds.ErrAccountPlanMissing)
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
			return res.SetSuccess(&providercmds.AddOK{ID: sub})
		},
	)
}
