package handlers

import (
	"fmt"

	uploadcmds "github.com/fil-forge/libforge/commands/upload"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"go.uber.org/zap"
)

// This handler removes an upload (root CID + shards mapping). It does NOT
// remove the shard blobs — blob removal is a separate per-digest /blob/remove
// decision owned by the client's reference accounting.
func NewUploadRemoveHandler(uploadStore upload_store.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", uploadcmds.Remove))
	return uploadcmds.Remove.Route(
		func(req *binding.Request[*uploadcmds.RemoveArguments], res *binding.Response[*uploadcmds.RemoveOK]) error {
			args := req.Task().Arguments()
			space := req.Invocation().Subject()
			log := log.With(
				zap.Stringer("space", space),
				zap.Stringer("root", args.Root),
			)
			log.Debug("removing upload")

			err := uploadStore.Remove(req.Context(), space, args.Root)
			if err != nil && !errors.Is(err, upload_store.ErrUploadNotFound) {
				log.Error("failed to remove upload", zap.Error(err))
				return fmt.Errorf("removing upload: %w", err)
			}

			// Removing an unknown upload is idempotent success.
			return res.SetSuccess(&uploadcmds.RemoveOK{})
		},
	)
}
