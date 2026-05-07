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

// WithAdminProviderDeregisterMethod registers the admin/provider/deregister handler.
func WithAdminProviderDeregisterMethod(id *identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		provider.DeregisterAbility,
		server.Provide(
			provider.Deregister,
			AdminProviderDeregisterHandler(id, providerStore, logger),
		),
	)
}

func AdminProviderDeregisterHandler(id *identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) server.HandlerFunc[provider.DeregisterCaveats, provider.DeregisterOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", provider.DeregisterAbility))
	return func(ctx context.Context,
		cap ucan.Capability[provider.DeregisterCaveats],
		inv invocation.Invocation,
		iCtx server.InvocationContext,
	) (result.Result[provider.DeregisterOk, failure.IPLDBuilderFailure], fx.Effects, error) {
		args := cap.Nb()
		if inv.Issuer().DID() != id.Signer.DID() {
			log.Warn("Unauthorized access attempt", zap.Stringer("issuer", inv.Issuer().DID()))
			return result.Error[provider.DeregisterOk, failure.IPLDBuilderFailure](
				errors.New("Unauthorized", "only the service identity can deregister a provider"),
			), nil, nil
		}

		err := providerStore.Delete(ctx, args.Provider)
		if err != nil {
			log.Error("Failed to deregister provider", zap.Error(err))
			return nil, nil, err
		}
		return result.Ok[provider.DeregisterOk, failure.IPLDBuilderFailure](provider.DeregisterOk{}), nil, nil
	}
}
