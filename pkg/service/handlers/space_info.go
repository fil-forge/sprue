package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/fil-forge/go-libstoracha/capabilities/space"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"go.uber.org/zap"
)

const SpaceUnknownErrorName = "SpaceUnknown"

// WithSpaceInfoMethod registers the space/info handler.
// This handler returns info about a space, including its providers.
func WithSpaceInfoMethod(provisioningSvc *provisioning.Service, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		space.InfoAbility,
		server.Provide(space.Info, SpaceInfoHandler(provisioningSvc, logger)),
	)
}

func SpaceInfoHandler(provisioningSvc *provisioning.Service, logger *zap.Logger) server.HandlerFunc[space.InfoCaveats, space.InfoOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", space.InfoAbility))
	return server.HandlerFunc[space.InfoCaveats, space.InfoOk, failure.IPLDBuilderFailure](
		func(ctx context.Context,
			cap ucan.Capability[space.InfoCaveats],
			inv invocation.Invocation,
			iCtx server.InvocationContext,
		) (result.Result[space.InfoOk, failure.IPLDBuilderFailure], fx.Effects, error) {
			log := log.With(zap.String("space", cap.With()))

			if !strings.HasPrefix(cap.With(), did.KeyPrefix) {
				return result.Error[space.InfoOk, failure.IPLDBuilderFailure](
					errors.New(SpaceUnknownErrorName, "can only get info for did:key spaces"),
				), nil, nil
			}

			spaceDID, err := did.Parse(cap.With())
			if err != nil {
				return result.Error[space.InfoOk, failure.IPLDBuilderFailure](
					errors.New(InvalidSpaceErrorName, "invalid space DID: %v", err),
				), nil, nil
			}

			log.Debug("getting space info")

			providers, err := provisioningSvc.ListServiceProviders(ctx, spaceDID)
			if err != nil {
				log.Error("failed to list service providers", zap.Error(err))
				return nil, nil, fmt.Errorf("listing service providers: %w", err)
			}

			if len(providers) == 0 {
				return result.Error[space.InfoOk, failure.IPLDBuilderFailure](
					errors.New(SpaceUnknownErrorName, "space not found"),
				), nil, nil
			}

			providerStrings := make([]string, 0, len(providers))
			for _, p := range providers {
				providerStrings = append(providerStrings, p.String())
			}

			return result.Ok[space.InfoOk, failure.IPLDBuilderFailure](space.InfoOk{
				Did:       spaceDID.String(),
				Providers: providerStrings,
			}), nil, nil
		})
}
