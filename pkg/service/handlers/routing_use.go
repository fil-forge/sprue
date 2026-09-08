package handlers

import (
	stderrors "errors"
	"fmt"

	routingcmds "github.com/fil-forge/libforge/commands/routing"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/routing"
	routingpolicy "github.com/fil-forge/sprue/pkg/store/routing_policy"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"go.uber.org/zap"
)

// NewRoutingUseHandler sets or clears the routing policy referenced by the
// space that is the invocation subject. The space must be provisioned with a
// provider, and a referenced policy must have a stored candidate set.
func NewRoutingUseHandler(provisioningSvc *provisioning.Service, router *routing.Service, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", routingcmds.Use))
	return routingcmds.Use.Route(
		func(req *binding.Request[*routingcmds.UseArguments], res *binding.Response[*routingcmds.UseOK]) error {
			space := req.Invocation().Subject()
			args := req.Task().Arguments()
			log := log.With(zap.Stringer("space", space))
			if args.Policy != nil {
				log = log.With(zap.Stringer("policy", *args.Policy))
			}
			log.Debug("using routing policy")

			providers, err := provisioningSvc.ListServiceProviders(req.Context(), space)
			if err != nil {
				log.Error("failed to list service providers", zap.Error(err))
				return fmt.Errorf("listing service providers: %w", err)
			}
			if len(providers) == 0 {
				log.Warn("space has no service provider")
				return res.SetFailure(errors.New(routingcmds.SpaceNotProvisionedErrorName, "space is not provisioned with a provider"))
			}

			if args.Policy == nil {
				if err := router.ClearSpacePolicy(req.Context(), space); err != nil {
					log.Error("failed to clear routing policy", zap.Error(err))
					return fmt.Errorf("clearing routing policy: %w", err)
				}
				return res.SetSuccess(&routingcmds.UseOK{})
			}

			err = router.UseSpacePolicy(req.Context(), space, *args.Policy, req.Invocation().Task().Link())
			if err != nil {
			if stderrors.Is(err, routingpolicy.ErrPolicyNotFound) {
				log.Warn("unknown routing policy")
				return res.SetFailure(errors.New(routingcmds.UnknownPolicyErrorName, fmt.Sprintf("routing policy %s not found", *args.Policy)))
			}
				log.Error("failed to set routing policy", zap.Error(err))
				return fmt.Errorf("setting routing policy: %w", err)
			}

			return res.SetSuccess(&routingcmds.UseOK{})
		},
	)
}
