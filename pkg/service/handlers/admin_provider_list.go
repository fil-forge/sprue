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
	"github.com/fil-forge/sprue/pkg/store"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
)

// WithAdminProviderListMethod registers the admin/provider/list handler.
func WithAdminProviderListMethod(id *identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		provider.ListAbility,
		server.Provide(
			provider.List,
			AdminProviderListHandler(id, providerStore, logger),
		),
	)
}

func AdminProviderListHandler(id *identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) server.HandlerFunc[provider.ListCaveats, provider.ListOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", provider.ListAbility))
	return func(ctx context.Context,
		cap ucan.Capability[provider.ListCaveats],
		inv invocation.Invocation,
		iCtx server.InvocationContext,
	) (result.Result[provider.ListOk, failure.IPLDBuilderFailure], fx.Effects, error) {
		if inv.Issuer().DID() != id.Signer.DID() {
			log.Warn("Unauthorized access attempt", zap.Stringer("issuer", inv.Issuer().DID()))
			return result.Error[provider.ListOk, failure.IPLDBuilderFailure](
				errors.New("Unauthorized", "only the service identity can list providers"),
			), nil, nil
		}

		records, err := store.Collect(ctx, func(ctx context.Context, options store.PaginationConfig) (store.Page[storageprovider.Record], error) {
			opts := []storageprovider.ListOption{}
			if options.Cursor != nil {
				opts = append(opts, storageprovider.WithListCursor(*options.Cursor))
			}
			return providerStore.List(ctx, opts...)
		})
		if err != nil {
			log.Error("Failed to list providers", zap.Error(err))
			return nil, nil, err
		}

		var providers []provider.Provider
		for _, p := range records {
			replicationWeight := p.Weight
			if p.ReplicationWeight != nil {
				replicationWeight = *p.ReplicationWeight
			}
			providers = append(providers, provider.Provider{
				ID:                p.Provider,
				Endpoint:          p.Endpoint.String(),
				Weight:            p.Weight,
				ReplicationWeight: replicationWeight,
			})
		}

		return result.Ok[provider.ListOk, failure.IPLDBuilderFailure](
			provider.ListOk{Providers: providers},
		), nil, nil
	}
}
