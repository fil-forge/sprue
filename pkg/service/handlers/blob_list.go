package handlers

import (
	"fmt"

	blobcaps "github.com/fil-forge/libforge/commands/blob"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"go.uber.org/zap"
)

func NewBlobListHandler(blobRegistry blobregistry.Store, logger *zap.Logger) Handler {
	log := logger.With(zap.Stringer("handler", blobcaps.List))
	return Handler{
		Command: blobcaps.List.Command,
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*blobcaps.ListArguments],
			res *bindexec.Response[*blobcaps.ListOK],
		) error {
			args := req.Task().Arguments()
			space := req.Invocation().Subject()
			log := log.With(zap.Stringer("space", space))

			var opts []blobregistry.ListOption
			if args.Size != nil {
				log = log.With(zap.Uint64("size", *args.Size))
				opts = append(opts, blobregistry.WithListLimit(int(*args.Size)))
			}
			if args.Cursor != nil {
				log = log.With(zap.String("cursor", *args.Cursor))
				opts = append(opts, blobregistry.WithListCursor(*args.Cursor))
			}
			log.Debug("listing blobs")

			page, err := blobRegistry.List(req.Context(), space, opts...)
			if err != nil {
				log.Error("failed to list blobs", zap.Error(err))
				return fmt.Errorf("listing blobs: %w", err)
			}

			results := make([]blobcaps.ListBlobItem, 0, len(page.Results))
			for _, r := range page.Results {
				results = append(results, blobcaps.ListBlobItem{
					Blob:       r.Blob,
					InsertedAt: r.InsertedAt.Unix(),
				})
			}

			return res.SetSuccess(&blobcaps.ListOK{
				Cursor:  page.Cursor,
				Results: results,
			})
		}),
	}
}
