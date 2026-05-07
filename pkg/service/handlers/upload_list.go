package handlers

import (
	"context"
	"fmt"

	"github.com/fil-forge/go-libstoracha/capabilities/upload"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"go.uber.org/zap"
)

// WithUploadAddMethod registers the upload/add handler.
// This handler registers an upload (root CID + shards mapping).
func WithUploadListMethod(uploadStore upload_store.Store, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		upload.AddAbility,
		server.Provide(upload.Add, UploadAddHandler(uploadStore, logger)),
	)
}

func UploadListHandler(uploadStore upload_store.Store, logger *zap.Logger) server.HandlerFunc[upload.ListCaveats, upload.ListOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", upload.ListAbility))
	return server.HandlerFunc[upload.ListCaveats, upload.ListOk, failure.IPLDBuilderFailure](
		func(ctx context.Context,
			cap ucan.Capability[upload.ListCaveats],
			inv invocation.Invocation,
			iCtx server.InvocationContext,
		) (result.Result[upload.ListOk, failure.IPLDBuilderFailure], fx.Effects, error) {
			args := cap.Nb()
			log := log.With(zap.String("space", cap.With()))

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

			space, err := did.Parse(cap.With())
			if err != nil {
				return result.Error[upload.ListOk, failure.IPLDBuilderFailure](
					errors.New(InvalidSpaceErrorName, "invalid space DID: %v", err),
				), nil, nil
			}

			page, err := uploadStore.List(ctx, space, opts...)
			if err != nil {
				log.Error("failed to llist uploads", zap.Error(err))
				return nil, nil, fmt.Errorf("listing uploads: %w", err)
			}

			results := make([]upload.ListItem, 0, len(page.Results))
			for _, r := range page.Results {
				results = append(results, upload.ListItem{
					Root:       cidlink.Link{Cid: r.Root},
					InsertedAt: r.InsertedAt,
					UpdatedAt:  r.UpdatedAt,
				})
			}

			return result.Ok[upload.ListOk, failure.IPLDBuilderFailure](upload.ListOk{
				Results: results,
				Cursor:  page.Cursor,
			}), nil, nil
		})
}
