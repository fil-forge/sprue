package handlers

import (
	"github.com/fil-forge/sprue/pkg/commands/admin/provider"
	"github.com/fil-forge/sprue/pkg/identity"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"go.uber.org/zap"
)

func NewAdminProviderDeregisterHandler(id *identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", provider.Deregister))
	return provider.Deregister.Route(
		func(req *binding.Request[*provider.DeregisterArguments], res *binding.Response[*provider.DeregisterOK]) error {
			args := req.Task().Arguments()

			if req.Invocation().Issuer() != id.Signer.DID() {
				log.Warn("Unauthorized access attempt", zap.Stringer("issuer", req.Invocation().Issuer()))
				return res.SetFailure(errors.New("Unauthorized", "only the service identity can deregister a provider"))
			}

			err := providerStore.Delete(req.Context(), args.Provider)
			if err != nil {
				if errors.Is(err, storageprovider.ErrStorageProviderNotFound) {
					log.Warn("Provider not found", zap.Stringer("provider", args.Provider))
					return res.SetFailure(err)
				}
				log.Error("Failed to deregister provider", zap.Error(err))
				return err
			}
			return res.SetSuccess(&provider.DeregisterOK{})
		},
	)
}
