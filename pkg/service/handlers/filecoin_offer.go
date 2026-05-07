package handlers

import (
	"context"

	"go.uber.org/zap"

	filecoincap "github.com/fil-forge/go-libstoracha/capabilities/filecoin"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
)

// WithFilecoinOfferMethod registers the filecoin/offer handler.
// This is a stub implementation that acknowledges Filecoin storage offers.
// TODO: Implement actual Filecoin deal making.
func WithFilecoinOfferMethod(logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		filecoincap.OfferAbility,
		server.Provide(
			filecoincap.Offer,
			func(ctx context.Context,
				cap ucan.Capability[filecoincap.OfferCaveats],
				inv invocation.Invocation,
				iCtx server.InvocationContext,
			) (result.Result[filecoincap.OfferOk, failure.IPLDBuilderFailure], fx.Effects, error) {
				spaceDID := cap.With()
				content := cap.Nb().Content
				piece := cap.Nb().Piece

				logger.Debug("filecoin/offer STUB (not implemented)",
					zap.String("space", spaceDID),
					zap.String("content", content.String()),
					zap.String("piece", piece.String()))

				// Return success echoing back the piece CID
				return result.Ok[filecoincap.OfferOk, failure.IPLDBuilderFailure](
					filecoincap.OfferOk{
						Piece: piece,
					},
				), nil, nil
			},
		),
	)
}
