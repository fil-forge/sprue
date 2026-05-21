package handlers

import (
	"fmt"

	cmdupload "github.com/fil-forge/libforge/commands/upload"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"go.uber.org/zap"
)

func NewUploadListHandler(uploadStore upload_store.Store, logger *zap.Logger) Handler {
	log := logger.With(zap.Stringer("handler", cmdupload.List))
	return Handler{
		Command: cmdupload.List.Command,
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*cmdupload.ListArguments],
			res *bindexec.Response[*cmdupload.ListOK],
		) error {
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

			results := make([]cmdupload.ListUploadItem, 0, len(page.Results))
			for _, r := range page.Results {
				results = append(results, cmdupload.ListUploadItem{
					Root:  r.Root,
					Index: r.Index,
				})
			}

			return res.SetSuccess(&cmdupload.ListOK{
				Results: results,
				Cursor:  page.Cursor,
			})
		}),
	}
}
