package handlers

import (
	"fmt"
	"strings"

	spacecmds "github.com/fil-forge/libforge/commands/space"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"go.uber.org/zap"
)

// This handler returns info about a space, including its providers.
func NewSpaceInfoHandler(provisioningSvc *provisioning.Service, logger *zap.Logger) Handler {
	log := logger.With(zap.Stringer("handler", spacecmds.Info))
	return Handler{
		Command: spacecmds.Info.Command,
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*spacecmds.InfoArguments],
			res *bindexec.Response[*spacecmds.InfoOK],
		) error {
			space := req.Invocation().Subject()
			log := log.With(zap.Stringer("space", space))
			log.Debug("getting space info")

			if !strings.HasPrefix(space.String(), "did:key:") {
				log.Warn("non-did:key space info requested")
				return res.SetFailure(errors.New(spacecmds.UnknownSpaceErrorName, "can only get info for did:key spaces"))
			}

			providers, err := provisioningSvc.ListServiceProviders(req.Context(), space)
			if err != nil {
				log.Error("failed to list service providers", zap.Error(err))
				return fmt.Errorf("listing service providers: %w", err)
			}

			return res.SetSuccess(&spacecmds.InfoOK{Providers: providers})
		}),
	}
}
