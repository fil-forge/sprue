package handlers

import (
	"net/url"

	"github.com/fil-forge/sprue/pkg/capabilities/admin/provider"
	"github.com/fil-forge/sprue/pkg/identity"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"go.uber.org/zap"
)

var (
	initialWeight            = 0
	initialReplicationWeight = 0
)

func NewAdminProviderRegisterHandler(id *identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) Handler {
	log := logger.With(zap.String("handler", provider.RegisterCommand))
	return Handler{
		Capability: provider.Register,
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*provider.RegisterArguments],
			res *bindexec.Response[*provider.RegisterOK],
		) error {
			args := req.Task().BindArguments()
			if req.Invocation().Issuer().DID() != id.Signer.DID() {
				log.Warn("Unauthorized access attempt", zap.Stringer("issuer", req.Invocation().Issuer().DID()))
				return res.SetFailure(errors.New("Unauthorized", "only the service identity can register providers"))
			}

			endpoint, err := url.Parse(args.Endpoint)
			if err != nil {
				log.Warn("Invalid endpoint", zap.String("endpoint", args.Endpoint), zap.Error(err))
				return res.SetFailure(errors.New("InvalidEndpoint", "parsing endpoint: %s", err.Error()))
			}

			_, err = providerStore.Get(req.Context(), args.Provider)
			if err != nil {
				if !errors.Is(err, storageprovider.ErrStorageProviderNotFound) {
					log.Error("Failed to get existing provider", zap.Error(err))
					return err
				}
			} else {
				log.Warn("Provider already registered", zap.Stringer("provider", args.Provider))
				return res.SetFailure(errors.New("ProviderAlreadyRegistered", "a provider with this DID is already registered"))
			}

			err = providerStore.Put(req.Context(), args.Provider, *endpoint, initialWeight, &initialReplicationWeight)
			if err != nil {
				log.Error("Failed to register provider", zap.Error(err))
				return err
			}
			return res.SetSuccess(&provider.RegisterOK{})
		}),
	}
}
