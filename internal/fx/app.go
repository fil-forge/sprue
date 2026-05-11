package fx

import (
	"fmt"

	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/internal/fx/service"
	"github.com/fil-forge/sprue/internal/fx/service/handlers"
	"github.com/fil-forge/sprue/internal/fx/store/aws"
	"github.com/fil-forge/sprue/internal/fx/store/memory"
	"github.com/fil-forge/sprue/internal/fx/store/postgres"
	"go.uber.org/fx"
)

// AppModule aggregates all application modules.
var AppModule = func(cfg *config.Config) fx.Option {
	opts := []fx.Option{
		fx.Supply(cfg),
		ConfigModule,
		LoggerModule,
		IdentityModule,
		ServicesModule,
		ClientsModule,
		service.Module,
		handlers.Module,
		ServerModule,
	}
	switch cfg.Storage.Type {
	case config.StorageTypeMemory:
		opts = append(opts, memory.Module)
	case config.StorageTypePostgres, "":
		// Empty Type is treated as the default backend (postgres) so callers
		// constructing a Config literal in tests don't have to set it.
		opts = append(opts, postgres.Module)
	case config.StorageTypeAWS:
		opts = append(opts, aws.Module)
	default:
		return fx.Error(fmt.Errorf("unknown storage.type %q (valid: memory, postgres, aws)", cfg.Storage.Type))
	}
	return fx.Options(opts...)
}
