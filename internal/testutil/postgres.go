package testutil

import (
	"testing"
	"time"

	"github.com/fil-forge/sprue/internal/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"
)

const (
	testPostgresDB   = "sprue"
	testPostgresUser = "sprue"
	testPostgresPass = "sprue"
)

// CreatePostgres starts a throwaway Postgres container, runs the sprue
// migrations against it, and returns a connection pool. The container is
// cleaned up when the test finishes.
func CreatePostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return createPostgres(t, 0)
}

// CreatePostgresAt is CreatePostgres stopped at a migration version, so a test
// can populate the schema as an older deployment left it and then apply the
// next migration itself.
func CreatePostgresAt(t *testing.T, version int64) *pgxpool.Pool {
	t.Helper()
	return createPostgres(t, version)
}

func createPostgres(t *testing.T, upTo int64) *pgxpool.Pool {
	t.Helper()

	ctx := t.Context()
	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase(testPostgresDB),
		tcpostgres.WithUsername(testPostgresUser),
		tcpostgres.WithPassword(testPostgresPass),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	t.Logf("Postgres DSN: %s", dsn)

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	if upTo > 0 {
		require.NoError(t, migrations.UpToDB(ctx, db, upTo, zap.NewNop()))
	} else {
		require.NoError(t, migrations.UpDB(ctx, db, zap.NewNop()))
	}
	return pool
}
