package handlers

import (
	"go.uber.org/zap"

	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/pkg/commands/admin/provider/proofs"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan/container"
)

// NewAdminProviderProofsSetHandler replaces the proofs a registered provider
// granted the service, checked against the same requirements as registration.
// The endpoint and weights are left as they are.
func NewAdminProviderProofsSetHandler(id identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", proofs.Set))
	return proofs.Set.Route(
		func(req *binding.Request[*proofs.SetArguments], res *binding.Response[*proofs.SetOK]) error {
			args := req.Task().Arguments()
			if req.Invocation().Issuer() != id.Issuer.DID() {
				log.Warn("Unauthorized access attempt", zap.Stringer("issuer", req.Invocation().Issuer()))
				return res.SetFailure(errors.New("Unauthorized", "only the service identity can set provider proofs"))
			}

			ct, err := container.Decode(args.Proofs)
			if err != nil {
				log.Warn("Invalid proofs", zap.Error(err))
				return res.SetFailure(errors.New("InvalidProofs", "decoding proofs: %s", err.Error()))
			}

			if err := verifyProviderProofs(req.Context(), id.Issuer.DID(), args.Provider, ct); err != nil {
				log.Warn("Invalid proofs", zap.Stringer("provider", args.Provider), zap.Error(err))
				return res.SetFailure(err)
			}

			err = providerStore.SetProofs(req.Context(), args.Provider, ct)
			if err != nil {
				if errors.Is(err, storageprovider.ErrStorageProviderNotFound) {
					log.Warn("Provider not found", zap.Stringer("provider", args.Provider))
					return res.SetFailure(errors.New("ProviderNotFound", "provider %s is not registered", args.Provider))
				}
				log.Error("Failed to update provider proofs", zap.Error(err))
				return err
			}

			log.Info("Provider proofs updated", zap.Stringer("provider", args.Provider))
			return res.SetSuccess(&proofs.SetOK{})
		},
	)
}
