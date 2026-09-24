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

// uploadDiffMigration is the version that adds the object-count log. Stopping
// one short of it leaves the schema as a deployment running without object
// counts would have it.
const uploadDiffMigration = 3

// TestUploadDiffBackfillsExistingUploads: the object count has to start from
// the uploads a database already holds. Without the backfill the counters
// start at zero however many exist, and removing a pre-existing upload records
// a -1 with no +1 to match, taking the reported count negative.
func TestUploadDiffBackfillsExistingUploads(t *testing.T) {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}

	ctx := t.Context()
	pool := testutil.CreatePostgresAt(t, uploadDiffMigration)

	// A space holding two uploads, as an older deployment left it.
	space := testutil.RandomDID(t).String()
	for range 2 {
		_, err := pool.Exec(ctx,
			`INSERT INTO upload (space, root, cause, inserted_at, updated_at)
			 VALUES ($1, $2, $3, NOW(), NOW())`,
			space, testutil.RandomCID(t).String(), testutil.RandomCID(t).String())
		require.NoError(t, err)
	}

	// Apply the object-count migration.
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	require.NoError(t, migrations.UpDB(ctx, db, zap.NewNop()))

	var addTotal int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT value FROM space_metrics WHERE space = $1 AND name = '/upload/add-total'`,
		space).Scan(&addTotal))
	require.EqualValues(t, 2, addTotal, "both existing uploads are counted")

	var admin int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT value FROM admin_metrics WHERE name = '/upload/add-total'`).Scan(&admin))
	require.EqualValues(t, 2, admin)

	// The diff log carries the same two, dated when the uploads arrived rather
	// than at the upgrade, so a reconstructed series puts them in the right
	// window. One row per upload, not one per the space's providers, or the
	// sum would not agree with the counter above.
	var diffs, sum int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(SUM(delta), 0) FROM upload_diff WHERE space = $1`,
		space).Scan(&diffs, &sum))
	require.EqualValues(t, 2, diffs)
	require.EqualValues(t, 2, sum)

	var sameInstant bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT bool_and(d.receipt_at = u.inserted_at)
		 FROM upload_diff d JOIN upload u ON u.space = d.space AND u.cause = d.cause
		 WHERE d.space = $1`, space).Scan(&sameInstant))
	require.True(t, sameInstant, "a backfilled delta is dated by its upload, not the upgrade")
}
