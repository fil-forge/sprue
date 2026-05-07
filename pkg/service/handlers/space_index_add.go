package handlers

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	spaceindexcap "github.com/fil-forge/go-libstoracha/capabilities/space/index"
	"github.com/fil-forge/go-ucanto/core/dag/blockstore"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/pkg/indexerclient"
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/provisioning"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
)

const IndexNotFoundErrorName = "IndexNotFound"

var ErrIndexNotFound = errors.New(IndexNotFoundErrorName, "index not found in space")

// extractRetrievalAuth extracts the space/content/retrieve delegation from the
// invocation facts. Guppy includes this delegation so the indexer can fetch
// the index blob from storage providers that require UCAN authorization.
func extractRetrievalAuth(inv invocation.Invocation) (delegation.Delegation, error) {
	var authLink ipld.Link
	for _, fact := range inv.Facts() {
		if v, ok := fact["retrievalAuth"]; ok {
			if node, ok := v.(ipld.Node); ok {
				link, err := node.AsLink()
				if err == nil {
					authLink = link
					break
				}
			}
		}
	}
	if authLink == nil {
		return nil, fmt.Errorf("retrievalAuth fact not found in invocation")
	}

	// Build delegation from invocation blocks
	bs, err := blockstore.NewBlockReader(blockstore.WithBlocksIterator(inv.Blocks()))
	if err != nil {
		return nil, fmt.Errorf("creating block reader: %w", err)
	}
	dlg, err := delegation.NewDelegationView(authLink, bs)
	if err != nil {
		return nil, fmt.Errorf("creating delegation view: %w", err)
	}
	return dlg, nil
}

// WithSpaceIndexAddMethod registers the space/index/add handler.
// This handler publishes index claims to the indexer service.
func WithSpaceIndexAddMethod(provisioningSvc *provisioning.Service, blobRegistry blobregistry.Store, indexerClient *indexerclient.Client, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		spaceindexcap.AddAbility,
		server.Provide(
			spaceindexcap.Add,
			SpaceIndexAddHandler(provisioningSvc, blobRegistry, indexerClient, logger),
		),
	)
}

func SpaceIndexAddHandler(provisioningSvc *provisioning.Service, blobRegistry blobregistry.Store, indexerClient *indexerclient.Client, logger *zap.Logger) server.HandlerFunc[spaceindexcap.AddCaveats, spaceindexcap.AddOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", spaceindexcap.AddAbility))
	return func(ctx context.Context,
		cap ucan.Capability[spaceindexcap.AddCaveats],
		inv invocation.Invocation,
		iCtx server.InvocationContext,
	) (result.Result[spaceindexcap.AddOk, failure.IPLDBuilderFailure], fx.Effects, error) {
		space, err := did.Parse(cap.With())
		if err != nil {
			return result.Error[spaceindexcap.AddOk, failure.IPLDBuilderFailure](
				errors.New(InvalidSpaceErrorName, "invalid space DID: %v", err),
			), nil, nil
		}
		index, err := ipldutil.ToCID(cap.Nb().Index)
		if err != nil {
			return nil, nil, err
		}
		content, err := ipldutil.ToCID(cap.Nb().Content)
		if err != nil {
			return nil, nil, err
		}

		log := log.With(
			zap.Stringer("space", space),
			zap.Stringer("index", index),
			zap.Stringer("content", content),
		)
		log.Debug("adding index")

		provs, err := provisioningSvc.ListServiceProviders(ctx, space)
		if err != nil {
			log.Error("failed to list service providers", zap.Error(err))
			return nil, nil, fmt.Errorf("listing service providers: %w", err)
		}
		if len(provs) == 0 {
			log.Warn("space has no service provider")
			return result.Error[spaceindexcap.AddOk, failure.IPLDBuilderFailure](
				errors.New(InsufficientStorageErrorName, "space has no service provider"),
			), nil, nil
		}

		// Ensure the index is stored in the agent's space
		_, err = blobRegistry.Get(ctx, space, index.Hash())
		if err != nil {
			if errors.Is(err, blobregistry.ErrEntryNotFound) {
				log.Warn("index not found in space")
				return result.Error[spaceindexcap.AddOk, failure.IPLDBuilderFailure](
					ErrIndexNotFound,
				), nil, nil
			}
			log.Error("failed to get index from blob registry", zap.Error(err))
			return nil, nil, err
		}

		// Extract retrievalAuth delegation from invocation facts
		// Guppy provides this so the indexer can fetch the index blob from piri
		retrievalAuth, err := extractRetrievalAuth(inv)
		if err != nil {
			log.Error("failed to extract retrieval auth", zap.Error(err))
			return nil, nil, fmt.Errorf("extracting retrieval auth: %w", err)
		}
		log.Debug("extracted retrieval auth", zap.Stringer("root", retrievalAuth.Link()))

		// Publish to indexer with retrieval authorization
		if err := indexerClient.PublishIndexClaim(ctx, space, content, index, retrievalAuth); err != nil {
			log.Error("failed to publish index claim", zap.Error(err))
			return nil, nil, fmt.Errorf("publishing index claim: %w", err)
		}

		return result.Ok[spaceindexcap.AddOk, failure.IPLDBuilderFailure](
			spaceindexcap.AddOk{},
		), nil, nil
	}
}
