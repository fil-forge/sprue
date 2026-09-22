package handlers_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fil-forge/libforge/attestation/didmailto"
	accesscmds "github.com/fil-forge/libforge/commands/access"
	metricscmds "github.com/fil-forge/libforge/commands/metrics"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	metrics_store "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	spacediff_store "github.com/fil-forge/sprue/pkg/store/space_diff/memory"
	subscription_store "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	"github.com/fil-forge/sprue/pkg/usage"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/server"
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

func (failingSpaceStore) Get(context.Context, did.DID, did.DID) (map[string]uint64, error) {
	return nil, fmt.Errorf("dial tcp 10.0.0.1:5432: connection refused")
}

// newMetricsSampleHandler wires the handler with a space provisioned to each of
// the given providers.
func newMetricsSampleHandler(
	t *testing.T,
	ctx context.Context,
	diffs *spacediff_store.Store,
	spaceMetrics *metrics_store.SpaceStore,
	now time.Time,
	space did.DID,
	providers ...did.DID,
) server.Route {
	t.Helper()
	logger := zaptest.NewLogger(t)
	provisioningSvc := provisioning.NewService(providers, consumer_store.New(), subscription_store.New())
	account := testutil.Must(didmailto.New("alice@example.com"))(t)
	for _, p := range providers {
		_, err := provisioningSvc.Provision(ctx, account, space, p, testutil.RandomCID(t))
		require.NoError(t, err)
	}
	svc := usage.NewService(diffs, spaceMetrics, logger, usage.WithClock(func() time.Time { return now }))
	return handlers.NewMetricsSampleHandler(svc, provisioningSvc, logger)
}

func TestMetricsSampleHandler(t *testing.T) {
	ctx := t.Context()

	uploadService := testutil.WebService
	alice := testutil.Alice
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(3 * time.Hour)

	args := func() *metricscmds.SampleArguments {
		return &metricscmds.SampleArguments{
			From:   from.Unix(),
			To:     to.Unix(),
			Window: int64(time.Hour / time.Second),
		}
	}

	t.Run("returns a sample per window", func(t *testing.T) {
		diffs := spacediff_store.New()
		spaceMetrics := metrics_store.NewSpaceStore()
		space := testutil.RandomIssuer(t)

		handler := newMetricsSampleHandler(
			t, ctx, diffs, spaceMetrics, to, space.DID(), uploadService.DID())

		require.NoError(t, diffs.Put(ctx, uploadService.DID(), space.DID(), "sub",
			testutil.RandomCID(t), 4096, from.Add(90*time.Minute)))
		require.NoError(t, spaceMetrics.IncrementTotals(ctx, uploadService.DID(), space.DID(), map[string]uint64{
			metrics.BlobAddTotalMetric:     1,
			metrics.BlobAddSizeTotalMetric: 4096,
		}))

		req, res := invokeMetricsSample(t, ctx, alice, uploadService, space, args())
		require.NoError(t, handler.Handler(req, res))

		ok, err := metricscmds.Sample.Unpack(res.Receipt())
		require.NoError(t, err)
		require.Equal(t, from.Unix(), ok.From)
		require.Equal(t, to.Unix(), ok.To)
		require.Equal(t, int64(3600), ok.Window)
		samples := ok.Samples.Entries[uploadService.DID()]
		require.Len(t, ok.Samples.Entries, 1)
		require.Len(t, samples, 3)
		require.Equal(t, from.Add(time.Hour).Unix(), samples[0].Timestamp)
		require.Equal(t, []uint64{0, 4096, 4096}, []uint64{
			samples[0].BytesStored, samples[1].BytesStored, samples[2].BytesStored,
		})
		require.Equal(t, uint64(4096), samples[1].BytesIngested)
	})

	t.Run("reads the provider the space was provisioned with", func(t *testing.T) {
		diffs := spacediff_store.New()
		spaceMetrics := metrics_store.NewSpaceStore()
		space := testutil.RandomIssuer(t)
		provider := testutil.RandomDID(t)

		handler := newMetricsSampleHandler(
			t, ctx, diffs, spaceMetrics, to, space.DID(), provider)

		// Recorded against the space's provider, which is not this service.
		require.NoError(t, diffs.Put(ctx, provider, space.DID(), "sub",
			testutil.RandomCID(t), 2048, from.Add(30*time.Minute)))
		require.NoError(t, spaceMetrics.IncrementTotals(ctx, provider, space.DID(), map[string]uint64{
			metrics.BlobAddTotalMetric:     1,
			metrics.BlobAddSizeTotalMetric: 2048,
		}))

		req, res := invokeMetricsSample(t, ctx, alice, uploadService, space, args())
		require.NoError(t, handler.Handler(req, res))

		ok, err := metricscmds.Sample.Unpack(res.Receipt())
		require.NoError(t, err)
		samples := ok.Samples.Entries[provider]
		require.Len(t, ok.Samples.Entries, 1)
		require.Equal(t, []uint64{2048, 2048, 2048}, []uint64{
			samples[0].BytesStored, samples[1].BytesStored, samples[2].BytesStored,
		})
	})

	t.Run("fails the invocation for a space with no provider", func(t *testing.T) {
		space := testutil.RandomIssuer(t)
		handler := newMetricsSampleHandler(
			t, ctx, spacediff_store.New(), metrics_store.NewSpaceStore(), to, space.DID())

		req, res := invokeMetricsSample(t, ctx, alice, uploadService, space, args())
		require.NoError(t, handler.Handler(req, res))

		_, err := metricscmds.Sample.Unpack(res.Receipt())
		var named errors.Named
		require.ErrorAs(t, err, &named)
		require.Equal(t, accesscmds.InsufficientStorageErrorName, named.Name())
	})

	t.Run("returns a series for every provider of the space", func(t *testing.T) {
		diffs := spacediff_store.New()
		spaceMetrics := metrics_store.NewSpaceStore()
		space := testutil.RandomIssuer(t)
		second := testutil.RandomDID(t)

		handler := newMetricsSampleHandler(
			t, ctx, diffs, spaceMetrics, to, space.DID(), uploadService.DID(), second)

		// One change, recorded by each provider against its own rows and totals.
		at := from.Add(30 * time.Minute)
		cause := testutil.RandomCID(t)
		for _, p := range []did.DID{uploadService.DID(), second} {
			require.NoError(t, diffs.Put(ctx, p, space.DID(), "sub", cause, 1024, at))
			require.NoError(t, spaceMetrics.IncrementTotals(ctx, p, space.DID(), map[string]uint64{
				metrics.BlobAddTotalMetric:     1,
				metrics.BlobAddSizeTotalMetric: 1024,
			}))
		}

		req, res := invokeMetricsSample(t, ctx, alice, uploadService, space, args())
		require.NoError(t, handler.Handler(req, res))

		ok, err := metricscmds.Sample.Unpack(res.Receipt())
		require.NoError(t, err)
		require.Len(t, ok.Samples.Entries, 2)
		// Both series cover the same grid and report the same stored bytes:
		// each describes the space from its own provider's side.
		for _, p := range []did.DID{uploadService.DID(), second} {
			samples := ok.Samples.Entries[p]
			require.Len(t, samples, 3, "provider %s", p)
			require.Equal(t, []uint64{1024, 1024, 1024}, []uint64{
				samples[0].BytesStored, samples[1].BytesStored, samples[2].BytesStored,
			}, "provider %s", p)
		}
	})

	t.Run("fails the invocation on an unusable range", func(t *testing.T) {
		space := testutil.RandomIssuer(t)
		handler := newMetricsSampleHandler(
			t, ctx, spacediff_store.New(), metrics_store.NewSpaceStore(), to, space.DID(),
			uploadService.DID())

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
		logger := zaptest.NewLogger(t)
		space := testutil.RandomIssuer(t)
		provisioningSvc := provisioning.NewService(
			[]did.DID{uploadService.DID()}, consumer_store.New(), subscription_store.New())
		account := testutil.Must(didmailto.New("alice@example.com"))(t)
		_, err := provisioningSvc.Provision(ctx, account, space.DID(), uploadService.DID(), testutil.RandomCID(t))
		require.NoError(t, err)

		svc := usage.NewService(
			spacediff_store.New(), failingSpaceStore{metrics_store.NewSpaceStore()}, logger,
			usage.WithClock(func() time.Time { return to }))
		handler := handlers.NewMetricsSampleHandler(svc, provisioningSvc, logger)

		req, res := invokeMetricsSample(t, ctx, alice, uploadService, space, args())

		err = handler.Handler(req, res)
		require.Error(t, err)
		// The connection detail belongs in the logs, not in a signed receipt.
		require.Contains(t, err.Error(), "sampling space usage")
	})
}
