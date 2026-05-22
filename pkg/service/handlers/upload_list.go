package handlers

import (
	"fmt"

	uploadcmds "github.com/fil-forge/libforge/commands/upload"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/server"
	"go.uber.org/zap"
)

func NewUploadListHandler(uploadStore upload_store.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", uploadcmds.List))
	return uploadcmds.List.Route(
		func(req *binding.Request[*uploadcmds.ListArguments], res *binding.Response[*uploadcmds.ListOK]) error {
			args := req.Task().Arguments()
			space := req.Invocation().Subject()
			log := log.With(zap.Stringer("space", space))

			var opts []upload_store.ListOption
			if args.Size != nil {
				log = log.With(zap.Uint64("size", *args.Size))
				opts = append(opts, upload_store.WithListLimit(int(*args.Size)))
			}
			if args.Cursor != nil {
				log = log.With(zap.String("cursor", *args.Cursor))
				opts = append(opts, upload_store.WithListCursor(*args.Cursor))
			}
			log.Debug("listing uploads")

			page, err := uploadStore.List(req.Context(), space, opts...)
			if err != nil {
				log.Error("failed to list uploads", zap.Error(err))
				return fmt.Errorf("listing uploads: %w", err)
			}

			results := make([]uploadcmds.ListUploadItem, 0, len(page.Results))
			for _, r := range page.Results {
				results = append(results, uploadcmds.ListUploadItem{
					Root:  r.Root,
					Index: r.Index,
				})
			}

			return res.SetSuccess(&uploadcmds.ListOK{
				Results: results,
				Cursor:  page.Cursor,
			})
		},
	)
}
