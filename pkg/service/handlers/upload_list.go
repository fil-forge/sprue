package handlers

import (
	"fmt"

	uploadcaps "github.com/fil-forge/libforge/capabilities/upload"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"go.uber.org/zap"
)

func NewUploadListHandler(uploadStore upload_store.Store, logger *zap.Logger) Handler {
	log := logger.With(zap.String("handler", uploadcaps.ListCommand))
	return Handler{
		Capability: uploadcaps.List,
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*uploadcaps.ListArguments],
			res *bindexec.Response[*uploadcaps.ListOK],
		) error {
			args := req.Task().BindArguments()
			space := req.Invocation().Subject()
			log := log.With(zap.Stringer("space", space.DID()))

			var opts []upload_store.ListOption
			if args.Size != nil {
				log = log.With(zap.Int64("size", *args.Size))
				opts = append(opts, upload_store.WithListLimit(int(*args.Size)))
			}
			if args.Cursor != nil {
				log = log.With(zap.String("cursor", *args.Cursor))
				opts = append(opts, upload_store.WithListCursor(*args.Cursor))
			}
			log.Debug("listing uploads")

			page, err := uploadStore.List(req.Context(), space.DID(), opts...)
			if err != nil {
				log.Error("failed to list uploads", zap.Error(err))
				return fmt.Errorf("listing uploads: %w", err)
			}

			results := make([]uploadcaps.ListUploadItem, 0, len(page.Results))
			for _, r := range page.Results {
				results = append(results, uploadcaps.ListUploadItem{
					Root:  r.Root,
					Index: r.Index,
				})
			}

			return res.SetSuccess(&uploadcaps.ListOK{
				Results: results,
				Cursor:  page.Cursor,
			})
		}),
	}
}
