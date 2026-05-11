package handlers

import (
	"github.com/fil-forge/sprue/pkg/capabilities/admin/provider"
	"github.com/fil-forge/sprue/pkg/identity"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"go.uber.org/zap"
)

func NewAdminProviderDeregisterHandler(id *identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) Handler {
	log := logger.With(zap.String("handler", provider.DeregisterCommand))
	return Handler{
		Capability: provider.Deregister,
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*provider.DeregisterArguments],
			res *bindexec.Response[*provider.DeregisterOK],
		) error {
			args := req.Task().BindArguments()

			if req.Invocation().Issuer().DID() != id.Signer.DID() {
				log.Warn("Unauthorized access attempt", zap.Stringer("issuer", req.Invocation().Issuer().DID()))
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
		}),
	}
}
