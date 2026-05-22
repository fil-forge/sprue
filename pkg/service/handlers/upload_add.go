package handlers

import (
	"fmt"

	accesscmds "github.com/fil-forge/libforge/commands/access"
	uploadcmds "github.com/fil-forge/libforge/commands/upload"
	"github.com/fil-forge/sprue/pkg/provisioning"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"go.uber.org/zap"
)

// This handler registers an upload (root CID + shards mapping).
func NewUploadAddHandler(provisioningSvc *provisioning.Service, uploadStore upload_store.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", uploadcmds.Add))
	return server.NewRoute(
		uploadcmds.Add,
		func(req *binding.Request[*uploadcmds.AddArguments], res *binding.Response[*uploadcmds.AddOK]) error {
			args := req.Task().Arguments()
			space := req.Invocation().Subject()
			cause := req.Invocation().Task().Link()
			log := log.With(
				zap.Stringer("space", space),
				zap.Stringer("root", args.Root),
			)
			if args.Index != nil {
				log = log.With(zap.Stringer("index", *args.Index))
			}
			log.Debug("adding upload")

			provs, err := provisioningSvc.ListServiceProviders(req.Context(), space)
			if err != nil {
				log.Error("failed to list service providers", zap.Error(err))
				return fmt.Errorf("listing service providers: %w", err)
			}
			if len(provs) == 0 {
				log.Warn("space has no service provider")
				return res.SetFailure(errors.New(accesscmds.InsufficientStorageErrorName, "space has no service provider"))
			}

			err = uploadStore.Upsert(req.Context(), space, args.Root, args.Index, args.Shards, cause)
			if err != nil {
				log.Error("failed to upsert upload", zap.Error(err))
				return fmt.Errorf("upserting upload: %w", err)
			}

			return res.SetSuccess(&uploadcmds.AddOK{})
		},
	)
}
