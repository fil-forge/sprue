package handlers

import (
	"fmt"

	"go.uber.org/zap"

	accesscmds "github.com/fil-forge/libforge/commands/access"
	indexcmds "github.com/fil-forge/libforge/commands/index"
	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/indexerclient"
	"github.com/fil-forge/sprue/pkg/provisioning"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
)

func NewIndexAddHandler(id *identity.Identity, provisioningSvc *provisioning.Service, blobRegistry blobregistry.Store, indexerClient *indexerclient.Client, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", indexcmds.Add))
	return server.NewRoute(
		indexcmds.Add,
		func(req *binding.Request[*indexcmds.AddArguments], res *binding.Response[*indexcmds.AddOK]) error {
			args := req.Task().Arguments()
			space := req.Invocation().Subject()
			index := args.Index

			log := log.With(
				zap.Stringer("space", space),
				zap.Stringer("index", index),
			)
			log.Debug("adding index")

			provs, err := provisioningSvc.ListServiceProviders(req.Context(), space)
			if err != nil {
				log.Error("failed to list service providers", zap.Error(err))
				return fmt.Errorf("listing service providers: %w", err)
			}
			if len(provs) == 0 {
				log.Warn("space has no service provider")
				return res.SetFailure(errors.New(accesscmds.InsufficientStorageErrorName, "space has no service provider"))
			}

			// Ensure the index is stored in the agent's space
			_, err = blobRegistry.Get(req.Context(), space, index.Hash())
			if err != nil {
				if errors.Is(err, blobregistry.ErrEntryNotFound) {
					log.Warn("index not found in space")
					return res.SetFailure(indexcmds.ErrIndexNotFound)
				}
				log.Error("failed to get index from blob registry", zap.Error(err))
				return err
			}

			// Request MUST include a delegation to the upload service that gives it
			// the ability to retrieve the index (a /content/retrieve delegation).
			// This is re-delegated to the indexer for indexing.
			proofStore := ucanlib.NewContainerProofStore(req.Metadata())
			// Publish to indexer with retrieval authorization
			if _, err := indexerClient.PublishIndexClaim(req.Context(), space, index, proofStore); err != nil {
				log.Error("failed to publish index claim", zap.Error(err))
				return fmt.Errorf("publishing index claim: %w", err)
			}

			return res.SetSuccess(&indexcmds.AddOK{})
		},
	)
}
