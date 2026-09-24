package postgres_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/fil-forge/sprue/internal/testutil"
	consumerpostgres "github.com/fil-forge/sprue/pkg/store/consumer/postgres"
	uploadpostgres "github.com/fil-forge/sprue/pkg/store/upload/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// TestRemoveOnASingleConnectionPool proves the store never needs two pooled
// connections at once. Collecting a space's consumers from inside the delete's
// transaction would hold one connection while waiting for another: with one
// connection it deadlocks outright, and with any pool it deadlocks once enough
// removes run at once to exhaust it.
func TestRemoveOnASingleConnectionPool(t *testing.T) {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}

	pool := testutil.CreatePostgres(t)
	single := singleConnPool(t, pool.Config().ConnString())

	consumers := consumerpostgres.New(single)
	store := uploadpostgres.New(single, consumers)

	space := testutil.RandomDID(t)
	root := testutil.RandomCID(t)
	require.NoError(t, consumers.Add(t.Context(), testutil.RandomDID(t), space, testutil.RandomDID(t), "sub1", testutil.RandomCID(t)))
	require.NoError(t, store.Upsert(t.Context(), space, root, nil, nil, testutil.RandomCID(t)))

	// A deadlock here hangs rather than fails, so the wait is bounded.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	require.NoError(t, store.Remove(ctx, space, root, testutil.RandomCID(t)))
	require.NoError(t, ctx.Err(), "remove must not need a second pooled connection")
}

func singleConnPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.MaxConns = 1
	cfg.MinConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}
