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
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload"
	"github.com/ipfs/go-cid"
	"go.uber.org/zap"
)

const InvalidSpaceErrorName = "InvalidSpace"

// WithUploadAddMethod registers the upload/add handler.
// This handler registers an upload (root CID + shards mapping).
func WithUploadAddMethod(uploadStore upload_store.Store, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		upload.AddAbility,
		server.Provide(upload.Add, UploadAddHandler(uploadStore, logger)),
	)
}

func UploadAddHandler(uploadStore upload_store.Store, logger *zap.Logger) server.HandlerFunc[upload.AddCaveats, upload.AddOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", upload.AddAbility))
	return server.HandlerFunc[upload.AddCaveats, upload.AddOk, failure.IPLDBuilderFailure](
		func(ctx context.Context,
			cap ucan.Capability[upload.AddCaveats],
			inv invocation.Invocation,
			iCtx server.InvocationContext,
		) (result.Result[upload.AddOk, failure.IPLDBuilderFailure], fx.Effects, error) {
			log := log.With(
				zap.String("space", cap.With()),
				zap.Stringer("root", cap.Nb().Root),
				zap.Int("shards", len(cap.Nb().Shards)),
			)
			log.Debug("adding upload")

			space, err := did.Parse(cap.With())
			if err != nil {
				return result.Error[upload.AddOk, failure.IPLDBuilderFailure](
					errors.New(InvalidSpaceErrorName, "invalid space DID: %v", err),
				), nil, nil
			}

			root, err := ipldutil.ToCID(cap.Nb().Root)
			if err != nil {
				return nil, nil, err
			}

			shards := make([]cid.Cid, 0, len(cap.Nb().Shards))
			for _, link := range cap.Nb().Shards {
				s, err := ipldutil.ToCID(link)
				if err != nil {
					return nil, nil, err
				}
				shards = append(shards, s)
			}

			cause, err := ipldutil.ToCID(inv.Link())
			if err != nil {
				return nil, nil, err
			}

			err = uploadStore.Upsert(ctx, space, root, shards, cause)
			if err != nil {
				log.Error("failed to upsert upload", zap.Error(err))
				return nil, nil, fmt.Errorf("upserting upload: %w", err)
			}

			return result.Ok[upload.AddOk, failure.IPLDBuilderFailure](upload.AddOk{
				Root: cap.Nb().Root,
			}), nil, nil
		})
}
