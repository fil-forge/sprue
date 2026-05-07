package handlers

import (
	"context"
	"fmt"

	"github.com/fil-forge/go-libstoracha/capabilities/space/blob"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"go.uber.org/zap"
)

// WithSpaceBlobListMethod registers the space/blob/list handler.
// This handler lists the blobs of a space.
func WithSpaceBlobListMethod(blobRegistry blobregistry.Store, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		blob.ListAbility,
		server.Provide(blob.List, SpaceBlobListHandler(blobRegistry, logger)),
	)
}

func SpaceBlobListHandler(blobRegistry blobregistry.Store, logger *zap.Logger) server.HandlerFunc[blob.ListCaveats, blob.ListOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", blob.ListAbility))
	return server.HandlerFunc[blob.ListCaveats, blob.ListOk, failure.IPLDBuilderFailure](
		func(ctx context.Context,
			cap ucan.Capability[blob.ListCaveats],
			inv invocation.Invocation,
			iCtx server.InvocationContext,
		) (result.Result[blob.ListOk, failure.IPLDBuilderFailure], fx.Effects, error) {
			args := cap.Nb()
			log := log.With(zap.String("space", cap.With()))

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

			space, err := did.Parse(cap.With())
			if err != nil {
				return result.Error[blob.ListOk, failure.IPLDBuilderFailure](
					errors.New(InvalidSpaceErrorName, "invalid space DID: %v", err),
				), nil, nil
			}

			page, err := blobRegistry.List(ctx, space, opts...)
			if err != nil {
				log.Error("failed to list blobs", zap.Error(err))
				return nil, nil, fmt.Errorf("listing blobs: %w", err)
			}

			results := make([]blob.ListBlobItem, 0, len(page.Results))
			for _, r := range page.Results {
				results = append(results, blob.ListBlobItem{
					Blob:       r.Blob,
					Cause:      cidlink.Link{Cid: r.Cause},
					InsertedAt: r.InsertedAt,
				})
			}

			return result.Ok[blob.ListOk, failure.IPLDBuilderFailure](blob.ListOk{
				Results: results,
				Cursor:  page.Cursor,
			}), nil, nil
		})
}
