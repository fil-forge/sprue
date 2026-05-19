package handlers

import (
	"fmt"

	shardcaps "github.com/fil-forge/libforge/commands/upload/shard"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"github.com/fil-forge/ucantone/ucan"
	"go.uber.org/zap"
)

// This handler lists the shards of an upload.
func NewUploadShardListHandler(uploadStore upload_store.Store, logger *zap.Logger) Handler {
	log := logger.With(zap.String("handler", string(shardcaps.List)))
	return Handler{
		Command: ucan.Command(shardcaps.List),
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*shardcaps.ListArguments],
			res *bindexec.Response[*shardcaps.ListOK],
		) error {
			args := req.Task().Arguments()
			space := req.Invocation().Subject()
			root := args.Root
			log := log.With(zap.Stringer("space", space), zap.Stringer("root", root))

			var opts []upload_store.ListShardsOption
			if args.Size != nil {
				log = log.With(zap.Uint64("size", *args.Size))
				opts = append(opts, upload_store.WithListShardsLimit(int(*args.Size)))
			}
			if args.Cursor != nil {
				log = log.With(zap.String("cursor", *args.Cursor))
				opts = append(opts, upload_store.WithListShardsCursor(*args.Cursor))
			}
			log.Debug("listing upload shards")

			page, err := uploadStore.ListShards(req.Context(), space, root, opts...)
			if err != nil {
				log.Error("failed to list upload shards", zap.Error(err))
				return fmt.Errorf("listing upload shards: %w", err)
			}

			return res.SetSuccess(&shardcaps.ListOK{
				Results: page.Results,
				Cursor:  page.Cursor,
			})
		}),
	}
}
