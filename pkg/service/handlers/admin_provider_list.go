package handlers

import (
	"context"

	"go.uber.org/zap"

	"github.com/fil-forge/sprue/pkg/capabilities/admin/provider"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/store"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/execution/bindexec"
)

func NewAdminProviderListHandler(id *identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) Handler {
	log := logger.With(zap.String("handler", provider.ListCommand))
	return Handler{
		Capability: provider.List,
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*provider.ListArguments],
			res *bindexec.Response[*provider.ListOK],
		) error {
			if req.Invocation().Issuer() != id.Signer.DID() {
				log.Warn("Unauthorized access attempt", zap.Stringer("issuer", req.Invocation().Issuer()))
				return res.SetFailure(errors.New("Unauthorized", "only the service identity can list providers"))
			}

			records, err := store.Collect(req.Context(), func(ctx context.Context, options store.PaginationConfig) (store.Page[storageprovider.Record], error) {
				opts := []storageprovider.ListOption{}
				if options.Cursor != nil {
					opts = append(opts, storageprovider.WithListCursor(*options.Cursor))
				}
				return providerStore.List(ctx, opts...)
			})
			if err != nil {
				log.Error("Failed to list providers", zap.Error(err))
				return err
			}

			var providers []provider.Provider
			for _, p := range records {
				replicationWeight := p.Weight
				if p.ReplicationWeight != nil {
					replicationWeight = *p.ReplicationWeight
				}
				providers = append(providers, provider.Provider{
					Provider:          p.Provider,
					Endpoint:          p.Endpoint.String(),
					Weight:            int64(p.Weight),
					ReplicationWeight: int64(replicationWeight),
				})
			}

			return res.SetSuccess(&provider.ListOK{Providers: providers})
		}),
	}
}
