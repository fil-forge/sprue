package migrations_test

import (
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/migrations"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// spaceDiffDropProviderMigration is the version that drops provider and
// subscription from the size log. Stopping one short of it leaves the schema as
// a deployment still recording them would have it.
const spaceDiffDropProviderMigration = 4

// TestSpaceDiffDropProviderCollapsesProviderCopies: dropping provider takes the
// primary key with it, and the key that replaces it cannot tell two providers'
// copies of one change apart. In practice no space ever had a second provider,
// so there is nothing to collapse in any real database, but the migration has
// to absorb the shape the old schema permitted rather than abort on it.
func TestSpaceDiffDropProviderCollapsesProviderCopies(t *testing.T) {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}

	ctx := t.Context()
	pool := testutil.CreatePostgresAt(t, spaceDiffDropProviderMigration)

	space := testutil.RandomDID(t).String()
	shared := testutil.RandomCID(t).String()
	replayed := testutil.RandomCID(t).String()
	first := testutil.RandomDID(t).String()
	second := testutil.RandomDID(t).String()

	// One change, recorded by two providers. inserted_at differs between the
	// copies, as a writer reading the clock per statement produced, so only a
	// key-wise dedup collapses them.
	for i, provider := range []string{first, second} {
		_, err := pool.Exec(ctx,
			`INSERT INTO space_diff (provider, space, receipt_at, cause, subscription, delta, inserted_at)
			 VALUES ($1, $2, '2026-01-01 00:00:00+00', $3, $4, 4096, $5)`,
			provider, space, shared, "sub", "2026-01-01 00:00:00.00000"+string(rune('1'+i))+"+00")
		require.NoError(t, err)
	}

	// One provider, one cause covering two changes at different instants: a
	// stored task re-invoked. Both must survive.
	for _, at := range []string{"2026-01-02 00:00:00+00", "2026-01-03 00:00:00+00"} {
		_, err := pool.Exec(ctx,
			`INSERT INTO space_diff (provider, space, receipt_at, cause, subscription, delta, inserted_at)
			 VALUES ($1, $2, $3, $4, $5, 512, NOW())`,
			first, space, at, replayed, "sub")
		require.NoError(t, err)
	}

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	require.NoError(t, migrations.UpDB(ctx, db, zap.NewNop()))

	var rows int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM space_diff WHERE space = $1`, space).Scan(&rows))
	require.EqualValues(t, 3, rows, "the two copies of one change collapse, the replayed cause does not")

	// Summing the copies instead of keeping one would double what the space is
	// held to store, which is what the usage service reads.
	var total int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT sum(delta) FROM space_diff WHERE space = $1`, space).Scan(&total))
	require.EqualValues(t, 4096+512+512, total)

	var dropped int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'space_diff' AND column_name IN ('provider', 'subscription')`).Scan(&dropped))
	require.Zero(t, dropped, "provider and subscription are gone")

	// The default 00003 set has to survive the rebuild.
	var def *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT column_default FROM information_schema.columns
		 WHERE table_name = 'space_diff' AND column_name = 'inserted_at'`).Scan(&def))
	require.NotNil(t, def)
	require.Equal(t, "now()", *def)
}
