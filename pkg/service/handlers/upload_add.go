package handlers

import (
	"fmt"

	cmdaccess "github.com/fil-forge/libforge/commands/access"
	cmdupload "github.com/fil-forge/libforge/commands/upload"
	"github.com/fil-forge/sprue/pkg/provisioning"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"go.uber.org/zap"
)

// This handler registers an upload (root CID + shards mapping).
func NewUploadAddHandler(provisioningSvc *provisioning.Service, uploadStore upload_store.Store, logger *zap.Logger) Handler {
	log := logger.With(zap.Stringer("handler", cmdupload.Add))
	return Handler{
		Command: cmdupload.Add.Command,
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*cmdupload.AddArguments],
			res *bindexec.Response[*cmdupload.AddOK],
		) error {
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
				return res.SetFailure(errors.New(cmdaccess.InsufficientStorageErrorName, "space has no service provider"))
			}

			err = uploadStore.Upsert(req.Context(), space, args.Root, args.Index, args.Shards, cause)
			if err != nil {
				log.Error("failed to upsert upload", zap.Error(err))
				return fmt.Errorf("upserting upload: %w", err)
			}

			return res.SetSuccess(&cmdupload.AddOK{})
		}),
	}
}
