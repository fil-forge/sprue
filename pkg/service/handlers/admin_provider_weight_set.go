package handlers

import (
	"go.uber.org/zap"

	"github.com/fil-forge/sprue/pkg/capabilities/admin/provider/weight"
	"github.com/fil-forge/sprue/pkg/identity"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/execution/bindexec"
)

func NewAdminProviderWeightSetHandler(id *identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) Handler {
	log := logger.With(zap.String("handler", weight.SetCommand))
	return Handler{
		Capability: weight.Set,
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*weight.SetArguments],
			res *bindexec.Response[*weight.SetOK],
		) error {
			args := req.Task().Arguments()
			if req.Invocation().Issuer() != id.Signer.DID() {
				log.Warn("Unauthorized access attempt", zap.Stringer("issuer", req.Invocation().Issuer()))
				return res.SetFailure(errors.New("Unauthorized", "only the service identity can set provider weights"))
			}

			p, err := providerStore.Get(req.Context(), args.Provider)
			if err != nil {
				log.Error("Failed to get existing provider", zap.Error(err))
				return res.SetFailure(errors.New("Failed to get existing provider", err.Error()))
			}

			replicationWeight := int(args.ReplicationWeight)
			err = providerStore.Put(req.Context(), p.Provider, p.Endpoint, int(args.Weight), &replicationWeight)
			if err != nil {
				if errors.Is(err, storageprovider.ErrStorageProviderNotFound) {
					log.Warn("Provider not found", zap.Stringer("provider", args.Provider))
					return res.SetFailure(err)
				}
				log.Error("Failed to update provider weights", zap.Error(err))
				return err
			}
			return res.SetSuccess(&weight.SetOK{})
		}),
	}
}
