package service

import (
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/indexerclient"
	"github.com/fil-forge/sprue/pkg/service"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/fil-forge/sprue/pkg/store/delegation"
)

// Module provides the UCAN service.
var Module = fx.Module("service",
	fx.Provide(NewService),
)

// ServiceParams groups dependencies for Service construction.
type ServiceParams struct {
	fx.In

	Identity        *identity.Identity
	AgentStore      agent.Store
	DelegationStore delegation.Store
	IndexerClient   *indexerclient.Client `optional:"true"`
	Logger          *zap.Logger
	Options         []server.Option `group:"ucan_options"`
}

// NewService creates the UCAN service with all handlers registered.
func NewService(p ServiceParams) (*service.Service, error) {
	return service.New(p.Identity, p.AgentStore, p.DelegationStore, p.IndexerClient, p.Logger, p.Options...)
}
