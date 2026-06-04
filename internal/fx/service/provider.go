package service

import (
	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/config"

	"github.com/fil-forge/sprue/pkg/service"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/fil-forge/sprue/pkg/store/delegation"
	"github.com/fil-forge/ucantone/server"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Module provides the UCAN service.
var Module = fx.Module("service",
	fx.Provide(NewService),
)

// ServiceParams groups dependencies for Service construction.
type ServiceParams struct {
	fx.In

	Identity         identity.Identity
	DeploymentConfig config.DeploymentConfig
	AgentStore       agent.Store
	DelegationStore  delegation.Store
	Logger           *zap.Logger
	Handlers         []server.Route      `group:"ucan_handlers"`
	Options          []server.HTTPOption `group:"ucan_options"`
}

// NewService creates the UCAN service with all handlers registered.
func NewService(p ServiceParams) (*service.Service, error) {
	return service.New(
		p.Identity,
		p.AgentStore,
		p.DelegationStore,
		p.Handlers,
		p.Logger,
		service.WithServerOptions(p.Options...),
		service.WithInsecureDIDResolution(p.DeploymentConfig.InsecureDIDResolution),
	)
}
