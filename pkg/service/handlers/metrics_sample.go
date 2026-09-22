package handlers

import (
	"fmt"
	"time"

	accesscmds "github.com/fil-forge/libforge/commands/access"
	metricscmds "github.com/fil-forge/libforge/commands/metrics"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/usage"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"go.uber.org/zap"
)

func NewMetricsSampleHandler(usageSvc *usage.Service, provisioningSvc *provisioning.Service, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", metricscmds.Sample))
	return metricscmds.Sample.Route(
		func(req *binding.Request[*metricscmds.SampleArguments], res *binding.Response[*metricscmds.SampleOK]) error {
			args := req.Task().Arguments()
			space := req.Invocation().Subject()
			log := log.With(
				zap.Stringer("space", space),
				zap.Int64("from", args.From),
				zap.Int64("to", args.To),
				zap.Int64("window", args.Window),
			)

			from, to, window, err := usage.ParseRange(args.From, args.To, args.Window)
			if err != nil {
				log.Debug("rejecting usage sample request", zap.Error(err))
				return res.SetFailure(err)
			}
			// Usage is recorded per provider, so the space's providers are what
			// the series are keyed by. A space with none has nothing to report.
			provs, err := provisioningSvc.ListServiceProviders(req.Context(), space)
			if err != nil {
				log.Error("failed to list service providers", zap.Error(err))
				return fmt.Errorf("listing service providers: %w", err)
			}
			if len(provs) == 0 {
				log.Warn("space has no service provider")
				return res.SetFailure(errors.New(accesscmds.InsufficientStorageErrorName, "space has no service provider"))
			}

			log = log.With(zap.Int("providers", len(provs)))
			log.Debug("sampling space usage")

			series, err := usageSvc.Sample(req.Context(), provs, space, from, to, window)
			if err != nil {
				// Named failures are the caller's to act on and travel in the
				// receipt. Anything else is a fault, and returning it keeps its
				// detail out of a signed record.
				var named errors.Named
				if errors.As(err, &named) {
					log.Debug("rejecting usage sample request", zap.Error(err))
					return res.SetFailure(named)
				}
				log.Error("failed to sample space usage", zap.Error(err))
				return fmt.Errorf("sampling space usage: %w", err)
			}

			entries := make(map[did.DID][]metricscmds.SampleItem, len(series.Samples))
			for provider, run := range series.Samples {
				samples := make([]metricscmds.SampleItem, 0, len(run))
				for _, s := range run {
					samples = append(samples, metricscmds.SampleItem{
						Timestamp:     s.End.Unix(),
						BytesStored:   s.BytesStored,
						BytesIngested: s.BytesIngested,
					})
				}
				entries[provider] = samples
			}

			return res.SetSuccess(&metricscmds.SampleOK{
				From:    series.From.Unix(),
				To:      series.To.Unix(),
				Window:  int64(series.Window / time.Second),
				Samples: metricscmds.SampleSet{Entries: entries},
			})
		},
	)
}
