package handlers

import (
	"context"

	"go.uber.org/zap"

	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/pkg/capabilities/admin/provider"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
)

// WithAdminProviderWeightSetMethod registers the admin/provider/weight/set handler.
func WithAdminProviderWeightSetMethod(id *identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		provider.WeightSetAbility,
		server.Provide(
			provider.WeightSet,
			AdminProviderWeightSetHandler(id, providerStore, logger),
		),
	)
}

func AdminProviderWeightSetHandler(id *identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) server.HandlerFunc[provider.WeightSetCaveats, provider.WeightSetOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", provider.WeightSetAbility))
	return func(ctx context.Context,
		cap ucan.Capability[provider.WeightSetCaveats],
		inv invocation.Invocation,
		iCtx server.InvocationContext,
	) (result.Result[provider.WeightSetOk, failure.IPLDBuilderFailure], fx.Effects, error) {
		args := cap.Nb()
		if inv.Issuer().DID() != id.Signer.DID() {
			log.Warn("Unauthorized access attempt", zap.Stringer("issuer", inv.Issuer().DID()))
			return result.Error[provider.WeightSetOk, failure.IPLDBuilderFailure](
				errors.New("Unauthorized", "only the service identity can set provider weights"),
			), nil, nil
		}

		p, err := providerStore.Get(ctx, args.Provider)
		if err != nil {
			log.Error("Failed to get existing provider", zap.Error(err))
			return nil, nil, err
		}

		err = providerStore.Put(ctx, p.Endpoint, p.Proof, args.Weight, &args.ReplicationWeight)
		if err != nil {
			log.Error("Failed to update provider weights", zap.Error(err))
			return nil, nil, err
		}
		return result.Ok[provider.WeightSetOk, failure.IPLDBuilderFailure](provider.WeightSetOk{}), nil, nil
	}
}
