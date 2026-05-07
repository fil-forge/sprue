package fx

import (
	"go.uber.org/fx"

	"github.com/fil-forge/sprue/internal/config"
)

var ConfigModule = fx.Module("config",
	fx.Provide(ProvideConfigs),
)

type Configs struct {
	fx.Out
	Deployment config.DeploymentConfig
	Server     config.ServerConfig
	Identity   config.IdentityConfig
	Indexer    config.IndexerConfig
	Storage    config.StorageConfig
	DynamoDB   config.DynamoDBConfig
	Postgres   config.PostgresConfig
	S3         config.S3Config
	Log        config.LogConfig
	Mailer     config.MailerConfig
}

// ProvideConfigs provides the individual fields of the config. Inner storage
// configs (Postgres, DynamoDB, S3) are surfaced flat so store providers can
// consume them directly without knowing about the Storage discriminator.
func ProvideConfigs(cfg *config.Config) Configs {
	return Configs{
		Deployment: cfg.Deployment,
		Server:     cfg.Server,
		Identity:   cfg.Identity,
		Indexer:    cfg.Indexer,
		Storage:    cfg.Storage,
		DynamoDB:   cfg.Storage.DynamoDB,
		Postgres:   cfg.Storage.Postgres,
		S3:         cfg.Storage.S3,
		Log:        cfg.Log,
		Mailer:     cfg.Mailer,
	}
}
