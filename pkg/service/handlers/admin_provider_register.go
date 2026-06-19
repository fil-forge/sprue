package handlers

import (
	"net/url"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	replicacmds "github.com/fil-forge/libforge/commands/blob/replica"
	pdpcmds "github.com/fil-forge/libforge/commands/pdp"
	"github.com/fil-forge/libforge/identity"
	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/sprue/pkg/commands/admin/provider"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"go.uber.org/zap"
)

var (
	initialWeight            = 0
	initialReplicationWeight = 0
)

// requiredProofs are the capabilities a registering provider must delegate to
// the service, identified by command.
var requiredProofs = []ucan.Command{
	blobcmds.Allocate.Command,
	blobcmds.Accept.Command,
	replicacmds.Allocate.Command,
	pdpcmds.Info.Command,
}

func NewAdminProviderRegisterHandler(id identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", provider.Register))
	return provider.Register.Route(
		func(req *binding.Request[*provider.RegisterArguments], res *binding.Response[*provider.RegisterOK]) error {
			args := req.Task().Arguments()
			if req.Invocation().Issuer() != id.Issuer.DID() {
				log.Warn("Unauthorized access attempt", zap.Stringer("issuer", req.Invocation().Issuer()))
				return res.SetFailure(errors.New("Unauthorized", "only the service identity can register providers"))
			}

			endpoint, err := url.Parse(args.Endpoint)
			if err != nil {
				log.Warn("Invalid endpoint", zap.String("endpoint", args.Endpoint), zap.Error(err))
				return res.SetFailure(errors.New("InvalidEndpoint", "parsing endpoint: %s", err.Error()))
			}

			proofs, err := container.Decode(args.Proofs)
			if err != nil {
				log.Warn("Invalid proofs", zap.Error(err))
				return res.SetFailure(errors.New("InvalidProofs", "decoding proofs: %s", err.Error()))
			}

			// Verify the proofs delegate every required capability from the
			// provider (subject) to the service (audience).
			proofStore := ucanlib.NewContainerProofStore(proofs)
			for _, cmd := range requiredProofs {
				chain, _, err := proofStore.ProofChain(req.Context(), id.Issuer.DID(), cmd, args.Provider)
				if err != nil {
					log.Error("Failed to build proof chain", zap.Stringer("command", cmd), zap.Error(err))
					return res.SetFailure(errors.New("InvalidProofs", "building proof chain for %s: %s", cmd, err.Error()))
				}
				if len(chain) == 0 {
					log.Warn("Missing required proof", zap.Stringer("command", cmd))
					return res.SetFailure(errors.New("InvalidProofs", "missing required %s delegation", cmd))
				}
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

			err = providerStore.Put(req.Context(), args.Provider, *endpoint, initialWeight, &initialReplicationWeight, proofs)
			if err != nil {
				log.Error("Failed to register provider", zap.Error(err))
				return err
			}
			return res.SetSuccess(&provider.RegisterOK{})
		},
	)
}
