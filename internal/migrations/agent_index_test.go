package migrations_test

import (
	"runtime"
	"testing"
	"time"

	"github.com/fil-forge/sprue/internal/migrations"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// beforeAgentIndexInsertedAt is the last schema version in which agent_index
// rows carry no date: the schema a deployment has immediately before the
// migration that adds the column.
const beforeAgentIndexInsertedAt = 5

// TestAgentIndexInsertedAtDatesExistingRows: a purge by age must be able to
// reach every row, including those written before the column existed. Those
// are dated at upgrade, which is the earliest the database can vouch for.
func TestAgentIndexInsertedAtDatesExistingRows(t *testing.T) {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}

	ctx := t.Context()
	pool := testutil.CreatePostgresAt(t, beforeAgentIndexInsertedAt)

	task := testutil.RandomCID(t).String()
	_, err := pool.Exec(ctx,
		`INSERT INTO agent_index (task, kind, token, message) VALUES ($1, 'in', $2, $3)`,
		task, testutil.RandomCID(t).String(), testutil.RandomCID(t).String())
	require.NoError(t, err)

	before := time.Now()
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	require.NoError(t, migrations.UpDB(ctx, db, zap.NewNop()))

	var insertedAt time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT inserted_at FROM agent_index WHERE task = $1 AND kind = 'in'`, task).Scan(&insertedAt))
	require.False(t, insertedAt.Before(before.Add(-time.Second)), "a pre-existing row is dated at upgrade")
	require.False(t, insertedAt.After(time.Now().Add(time.Second)))

	// A row written through the existing INSERT, which names no inserted_at,
	// takes it from the default.
	later := testutil.RandomCID(t).String()
	_, err = pool.Exec(ctx,
		`INSERT INTO agent_index (task, kind, token, message) VALUES ($1, 'out', $2, $3)`,
		later, testutil.RandomCID(t).String(), testutil.RandomCID(t).String())
	require.NoError(t, err)
	var laterAt time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT inserted_at FROM agent_index WHERE task = $1 AND kind = 'out'`, later).Scan(&laterAt))
	require.False(t, laterAt.Before(insertedAt), "a new row is dated no earlier than the upgrade")

	var indexed bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes
		 WHERE tablename = 'agent_index' AND indexname = 'agent_index_inserted_at_idx')`).Scan(&indexed))
	require.True(t, indexed, "the purge's range column is indexed")
}
