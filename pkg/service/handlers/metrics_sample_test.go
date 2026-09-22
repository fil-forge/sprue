package handlers_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	metricscmds "github.com/fil-forge/libforge/commands/metrics"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	metrics_store "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	spacediff_store "github.com/fil-forge/sprue/pkg/store/space_diff/memory"
	"github.com/fil-forge/sprue/pkg/usage"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func invokeMetricsSample(
	t *testing.T,
	ctx context.Context,
	agent ucan.Issuer,
	uploadService ucan.Issuer,
	space ucan.Principal,
	args *metricscmds.SampleArguments,
) (execution.Request, *execution.ExecResponse) {
	t.Helper()
	inv, err := metricscmds.Sample.Invoke(
		agent,
		space.DID(),
		args,
		invocation.WithAudience(uploadService.DID()),
	)
	require.NoError(t, err)
	req := execution.NewRequest(ctx, inv)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
	require.NoError(t, err)
	return req, res
}

// failingSpaceStore stands in for a store that is down.
type failingSpaceStore struct {
	metrics.SpaceStore
}

func (failingSpaceStore) Get(context.Context, did.DID) (map[string]uint64, error) {
	return nil, fmt.Errorf("dial tcp 10.0.0.1:5432: connection refused")
}

func TestMetricsSampleHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService
	alice := testutil.Alice
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(3 * time.Hour)

	t.Run("returns a sample per window", func(t *testing.T) {
		diffs := spacediff_store.New()
		spaceMetrics := metrics_store.NewSpaceStore()
		svc := usage.NewService(uploadService, diffs, spaceMetrics, logger,
			usage.WithClock(func() time.Time { return to }))
		handler := handlers.NewMetricsSampleHandler(svc, logger)

		space := testutil.RandomIssuer(t)
		require.NoError(t, diffs.Put(ctx, uploadService.DID(), space.DID(), "sub",
			testutil.RandomCID(t), 4096, from.Add(90*time.Minute)))
		require.NoError(t, spaceMetrics.IncrementTotals(ctx, space.DID(), map[string]uint64{
			metrics.BlobAddTotalMetric:     1,
			metrics.BlobAddSizeTotalMetric: 4096,
		}))

		req, res := invokeMetricsSample(t, ctx, alice, uploadService, space, &metricscmds.SampleArguments{
			From:   from.Unix(),
			To:     to.Unix(),
			Window: int64(time.Hour / time.Second),
		})

		require.NoError(t, handler.Handler(req, res))

		ok, err := metricscmds.Sample.Unpack(res.Receipt())
		require.NoError(t, err)
		require.Equal(t, from.Unix(), ok.From)
		require.Equal(t, to.Unix(), ok.To)
		require.Equal(t, int64(3600), ok.Window)
		require.Len(t, ok.Samples, 3)
		require.Equal(t, from.Add(time.Hour).Unix(), ok.Samples[0].Timestamp)
		require.Equal(t, []uint64{0, 4096, 4096}, []uint64{
			ok.Samples[0].BytesStored, ok.Samples[1].BytesStored, ok.Samples[2].BytesStored,
		})
		require.Equal(t, uint64(4096), ok.Samples[1].BytesIngested)
	})

	t.Run("fails the invocation on an unusable range", func(t *testing.T) {
		svc := usage.NewService(uploadService, spacediff_store.New(), metrics_store.NewSpaceStore(), logger,
			usage.WithClock(func() time.Time { return to }))
		handler := handlers.NewMetricsSampleHandler(svc, logger)

		space := testutil.RandomIssuer(t)
		req, res := invokeMetricsSample(t, ctx, alice, uploadService, space, &metricscmds.SampleArguments{
			From:   to.Unix(),
			To:     from.Unix(),
			Window: int64(time.Hour / time.Second),
		})

		require.NoError(t, handler.Handler(req, res))

		_, err := metricscmds.Sample.Unpack(res.Receipt())
		var named errors.Named
		require.ErrorAs(t, err, &named)
		require.Equal(t, metricscmds.InvalidRangeErrorName, named.Name())
	})

	t.Run("faults rather than signing a receipt when the store is down", func(t *testing.T) {
		svc := usage.NewService(uploadService, spacediff_store.New(),
			failingSpaceStore{metrics_store.NewSpaceStore()}, logger,
			usage.WithClock(func() time.Time { return to }))
		handler := handlers.NewMetricsSampleHandler(svc, logger)

		space := testutil.RandomIssuer(t)
		req, res := invokeMetricsSample(t, ctx, alice, uploadService, space, &metricscmds.SampleArguments{
			From:   from.Unix(),
			To:     to.Unix(),
			Window: int64(time.Hour / time.Second),
		})

		err := handler.Handler(req, res)
		require.Error(t, err)
		// The connection detail belongs in the logs, not in a signed receipt.
		require.Contains(t, err.Error(), "sampling space usage")
	})
}
