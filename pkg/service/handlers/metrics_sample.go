package handlers

import (
	"fmt"
	"time"

	metricscmds "github.com/fil-forge/libforge/commands/metrics"
	"github.com/fil-forge/sprue/pkg/usage"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"go.uber.org/zap"
)

func NewMetricsSampleHandler(usageSvc *usage.Service, logger *zap.Logger) server.Route {
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
			log.Debug("sampling space usage")

			series, err := usageSvc.Sample(req.Context(), space, from, to, window)
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

			samples := make([]metricscmds.SampleItem, 0, len(series.Samples))
			for _, s := range series.Samples {
				samples = append(samples, metricscmds.SampleItem{
					Timestamp:     s.End.Unix(),
					BytesStored:   s.BytesStored,
					BytesIngested: s.BytesIngested,
				})
			}

			return res.SetSuccess(&metricscmds.SampleOK{
				From:    series.From.Unix(),
				To:      series.To.Unix(),
				Window:  int64(series.Window / time.Second),
				Samples: samples,
			})
		},
	)
}
