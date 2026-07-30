package postgres_test

import (
	"context"
	"testing"

	metricspostgres "github.com/fil-forge/sprue/pkg/store/metrics/postgres"
	"github.com/fil-forge/ucantone/did"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// recordingExec is a stub pgxExec that records the metric name passed to each
// Exec call. The metric name is the last positional argument for both admin and
// space upserts.
type recordingExec struct {
	metrics []string
}

func (r *recordingExec) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	// The metric name is always the argument immediately before the value.
	metric := args[len(args)-2].(string)
	r.metrics = append(r.metrics, metric)
	return pgconn.CommandTag{}, nil
}

func TestIncrementAdminWithSortsKeys(t *testing.T) {
	rec := &recordingExec{}
	inc := map[string]uint64{"zebra": 1, "alpha": 2, "mike": 3}
	require.NoError(t, metricspostgres.IncrementAdminWith(context.Background(), rec, inc))
	require.Equal(t, []string{"alpha", "mike", "zebra"}, rec.metrics)
}

func TestIncrementSpaceWithSortsKeys(t *testing.T) {
	space, err := did.Parse("did:web:example.com")
	require.NoError(t, err)

	rec := &recordingExec{}
	inc := map[string]uint64{"zebra": 1, "alpha": 2, "mike": 3}
	require.NoError(t, metricspostgres.IncrementSpaceWith(context.Background(), rec, space, inc))
	require.Equal(t, []string{"alpha", "mike", "zebra"}, rec.metrics)
}
