package fx

import (
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/fil-forge/sprue/internal/config"
)

// LoggerModule provides the zap logger.
var LoggerModule = fx.Module("logger",
	fx.Provide(NewLogger),
)

// NewLogger creates a zap logger based on the configured log level.
func NewLogger(cfg *config.Config) (*zap.Logger, error) {
	if cfg.Log.Level == "debug" || cfg.Deployment.Environment == "development" || cfg.Deployment.Environment == "test" {
		return zap.NewDevelopment()
	}
	return zap.NewProduction()
}
