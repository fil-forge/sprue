package handlers

import (
	"go.uber.org/zap"

	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/pkg/commands/admin/provider/weight"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
)

func NewAdminProviderWeightSetHandler(id identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", weight.Set))
	return weight.Set.Route(
		func(req *binding.Request[*weight.SetArguments], res *binding.Response[*weight.SetOK]) error {
			args := req.Task().Arguments()
			if req.Invocation().Issuer() != id.Issuer.DID() {
				log.Warn("Unauthorized access attempt", zap.Stringer("issuer", req.Invocation().Issuer()))
				return res.SetFailure(errors.New("Unauthorized", "only the service identity can set provider weights"))
			}

			replicationWeight := int(args.ReplicationWeight)
			err := providerStore.SetWeights(req.Context(), args.Provider, int(args.Weight), &replicationWeight)
			if err != nil {
				if errors.Is(err, storageprovider.ErrStorageProviderNotFound) {
					log.Warn("Provider not found", zap.Stringer("provider", args.Provider))
					return res.SetFailure(errors.New("Failed to get existing provider", err.Error()))
				}
				log.Error("Failed to update provider weights", zap.Error(err))
				return err
			}
			return res.SetSuccess(&weight.SetOK{})
		},
	)
}
