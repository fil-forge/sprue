// Package postgres wires the Postgres-backed store implementations into the
// application via uber-go/fx. It mirrors the layout of
// internal/fx/store/aws/provider.go.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/internal/migrations"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fil-forge/sprue/pkg/store/agent"
	pgagent "github.com/fil-forge/sprue/pkg/store/agent/postgres"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	pgblobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry/postgres"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	pgconsumer "github.com/fil-forge/sprue/pkg/store/consumer/postgres"
	"github.com/fil-forge/sprue/pkg/store/customer"
	pgcustomer "github.com/fil-forge/sprue/pkg/store/customer/postgres"
	"github.com/fil-forge/sprue/pkg/store/delegation"
	pgdelegation "github.com/fil-forge/sprue/pkg/store/delegation/postgres"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	pgmetrics "github.com/fil-forge/sprue/pkg/store/metrics/postgres"
	"github.com/fil-forge/sprue/pkg/store/replica"
	pgreplica "github.com/fil-forge/sprue/pkg/store/replica/postgres"
	"github.com/fil-forge/sprue/pkg/store/revocation"
	pgrevocation "github.com/fil-forge/sprue/pkg/store/revocation/postgres"
	spacediff "github.com/fil-forge/sprue/pkg/store/space_diff"
	pgspacediff "github.com/fil-forge/sprue/pkg/store/space_diff/postgres"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	pgstorageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider/postgres"
	"github.com/fil-forge/sprue/pkg/store/subscription"
	pgsubscription "github.com/fil-forge/sprue/pkg/store/subscription/postgres"
	"github.com/fil-forge/sprue/pkg/store/upload"
	pgupload "github.com/fil-forge/sprue/pkg/store/upload/postgres"

	// Reuse the AWS S3 client constructor for the three stores that keep an S3 half.
	awsstore "github.com/fil-forge/sprue/internal/fx/store/aws"

	"go.uber.org/fx"
	"go.uber.org/zap"
)

var Module = fx.Module("postgres-store",
	fx.Provide(
		NewPostgresPool,
		NewMigratedPool,
		// S3 client is still needed for the three stores (agent, delegation, upload)
		// that persist blob payloads outside of the database.
		awsstore.NewS3Client,

		fx.Annotate(NewAgentStore, fx.As(new(agent.Store))),
		fx.Annotate(NewBlobRegistryStore, fx.As(new(blobregistry.Store))),
		fx.Annotate(NewConsumerStore, fx.As(new(consumer.Store))),
		fx.Annotate(NewCustomerStore, fx.As(new(customer.Store))),
		fx.Annotate(NewDelegationStore, fx.As(new(delegation.Store))),
		fx.Annotate(NewSpaceMetricsStore, fx.As(fx.Self()), fx.As(new(metrics.SpaceStore))),
		fx.Annotate(NewAdminMetricsStore, fx.As(fx.Self()), fx.As(new(metrics.Store))),
		fx.Annotate(NewReplicaStore, fx.As(new(replica.Store))),
		fx.Annotate(NewRevocationStore, fx.As(new(revocation.Store))),
		fx.Annotate(NewSpaceDiffStore, fx.As(fx.Self()), fx.As(new(spacediff.Store))),
		fx.Annotate(NewStorageProviderStore, fx.As(new(storageprovider.Store))),
		fx.Annotate(NewSubscriptionStore, fx.As(new(subscription.Store))),
		fx.Annotate(NewUploadStore, fx.As(new(upload.Store))),
	),
)

// MigratedPool is a *pgxpool.Pool whose schema is guaranteed to be at the head
// revision of internal/migrations by the time any OnStart hook that depends on
// it runs. Store constructors depend on *MigratedPool rather than
// *pgxpool.Pool so the fx dependency graph orders NewMigratedPool's migration
// hook before every store's Initialize hook.
type MigratedPool struct {
	*pgxpool.Pool
}

// NewPostgresPool creates a pgx connection pool and registers a lifecycle hook
// to close it at shutdown.
func NewPostgresPool(cfg config.PostgresConfig, lc fx.Lifecycle, logger *zap.Logger) (*pgxpool.Pool, error) {
	if cfg.DSN == "" {
		return nil, errors.New("postgres.dsn is required when store_backend is \"postgres\"")
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parsing postgres DSN: %w", err)
	}
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating pgx pool: %w", err)
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				return fmt.Errorf("pinging postgres: %w", err)
			}
			logger.Info("connected to postgres", zap.Int32("max_conns", poolCfg.MaxConns))
			return nil
		},
		OnStop: func(ctx context.Context) error {
			pool.Close()
			return nil
		},
	})

	return pool, nil
}

// NewMigratedPool registers an OnStart hook that runs goose migrations against
// pool, and returns a *MigratedPool wrapper. Because every store constructor
// depends on *MigratedPool, fx resolves this provider (and appends its OnStart
// hook) before any store's Initialize hook is registered — and OnStart hooks
// fire in registration order — so all stores see a fully migrated schema.
// Migrations are skipped when storage.postgres.skip_migrations is true.
func NewMigratedPool(lc fx.Lifecycle, cfg config.PostgresConfig, pool *pgxpool.Pool, logger *zap.Logger) *MigratedPool {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if cfg.SkipMigrations {
				logger.Info("skipping postgres migrations (storage.postgres.skip_migrations=true)")
				return nil
			}
			logger.Info("running postgres migrations")
			return migrations.Up(ctx, pool, logger)
		},
	})
	return &MigratedPool{Pool: pool}
}

func NewAgentStore(lc fx.Lifecycle, mdb *MigratedPool, s3Cfg config.S3Config, s3Client *s3.Client) agent.Store {
	store := pgagent.New(mdb.Pool, s3Client, s3Cfg.AgentMessageBucket)
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return store.Initialize(ctx)
		},
		OnStop: func(ctx context.Context) error {
			return store.Shutdown(ctx)
		},
	})
	return store
}

func NewBlobRegistryStore(mdb *MigratedPool, consumerStore consumer.Store) blobregistry.Store {
	return pgblobregistry.New(mdb.Pool, consumerStore)
}

func NewConsumerStore(mdb *MigratedPool) consumer.Store {
	return pgconsumer.New(mdb.Pool)
}

func NewCustomerStore(mdb *MigratedPool) customer.Store {
	return pgcustomer.New(mdb.Pool)
}

func NewDelegationStore(lc fx.Lifecycle, mdb *MigratedPool, s3Cfg config.S3Config, s3Client *s3.Client) delegation.Store {
	store := pgdelegation.New(mdb.Pool, s3Client, s3Cfg.DelegationBucket)
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return store.Initialize(ctx)
		},
	})
	return store
}

func NewSpaceMetricsStore(mdb *MigratedPool) *pgmetrics.SpaceStore {
	return pgmetrics.NewSpaceStore(mdb.Pool)
}

func NewAdminMetricsStore(mdb *MigratedPool) *pgmetrics.Store {
	return pgmetrics.New(mdb.Pool)
}

func NewReplicaStore(mdb *MigratedPool) replica.Store {
	return pgreplica.New(mdb.Pool)
}

func NewRevocationStore(mdb *MigratedPool) revocation.Store {
	return pgrevocation.New(mdb.Pool)
}

func NewSpaceDiffStore(mdb *MigratedPool) *pgspacediff.Store {
	return pgspacediff.New(mdb.Pool)
}

func NewStorageProviderStore(mdb *MigratedPool) storageprovider.Store {
	return pgstorageprovider.New(mdb.Pool)
}

func NewSubscriptionStore(mdb *MigratedPool) subscription.Store {
	return pgsubscription.New(mdb.Pool)
}

func NewUploadStore(mdb *MigratedPool) upload.Store {
	return pgupload.New(mdb.Pool)
}
