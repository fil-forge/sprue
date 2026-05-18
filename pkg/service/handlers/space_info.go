package handlers

import (
	"fmt"
	"strings"

	spacecaps "github.com/fil-forge/libforge/capabilities/space"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"go.uber.org/zap"
)

// This handler returns info about a space, including its providers.
func NewSpaceInfoHandler(provisioningSvc *provisioning.Service, logger *zap.Logger) Handler {
	log := logger.With(zap.String("handler", spacecaps.InfoCommand))
	return Handler{
		Capability: spacecaps.Info,
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*spacecaps.InfoArguments],
			res *bindexec.Response[*spacecaps.InfoOK],
		) error {
			space := req.Invocation().Subject()
			log := log.With(zap.Stringer("space", space))
			log.Debug("getting space info")

			if !strings.HasPrefix(space.String(), "did:key:") {
				log.Warn("non-did:key space info requested")
				return res.SetFailure(errors.New(spacecaps.UnknownSpaceErrorName, "can only get info for did:key spaces"))
			}

			providers, err := provisioningSvc.ListServiceProviders(req.Context(), space)
			if err != nil {
				log.Error("failed to list service providers", zap.Error(err))
				return fmt.Errorf("listing service providers: %w", err)
			}

			return res.SetSuccess(&spacecaps.InfoOK{Providers: providers})
		}),
	}
}
