package handlers

import (
	"context"
	"fmt"

	blobcaps "github.com/fil-forge/libforge/capabilities/blob"
	httpcaps "github.com/fil-forge/libforge/capabilities/http"
	ucancaps "github.com/fil-forge/libforge/capabilities/ucan"
	"github.com/fil-forge/libforge/digestutil"
	"github.com/fil-forge/sprue/pkg/lib/ucan_server"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/store/agent"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	"github.com/fil-forge/ucantone/errors"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/ipld"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/result"
	"github.com/fil-forge/ucantone/ucan"
	"go.uber.org/zap"
)

func NewHTTPPutConcludeHandler(
	router *routing.Service,
	nodeProvider piriclient.Provider,
	agentStore agent.Store,
	blobRegistry blobregistry.Store,
	logger *zap.Logger,
) ConclusionHandler {
	log := logger.With(
		zap.String("handler", ucancaps.ConcludeCommand),
		zap.String("conclude", httpcaps.PutCommand),
	)
	return ConclusionHandler{
		Command: httpcaps.PutCommand,
		Handler: func(ctx context.Context, putInv ucan.Invocation, putRcpt ucan.Receipt, meta ucan.Container) error {
			log := log.With(zap.Stringer("ran", putRcpt.Ran()))
			log.Debug("handling conclude")

			putArgs := httpcaps.PutArguments{}
			err := datamodel.Rebind(datamodel.NewAny(putInv.Arguments()), &putArgs)
			if err != nil {
				log.Error("failed to rebind HTTP PUT arguments", zap.Error(err))
				return fmt.Errorf("rebinding HTTP PUT arguments: %w", err)
			}

			allocTaskLink := putArgs.Destination.Task
			log = log.With(zap.Stringer("allocation", allocTaskLink))

			allocInv, err := agentStore.GetInvocation(ctx, allocTaskLink)
			if err != nil {
				log.Error("failed to get allocation invocation", zap.Error(err))
				return fmt.Errorf("getting allocation invocation: %w", err)
			}

			provider := allocInv.Audience()
			if provider == nil {
				// shouldn't happen, subject should be the space and audience the node
				provider = allocInv.Subject()
			}
			space := allocInv.Subject()

			log = log.With(
				zap.Stringer("space", space.DID()),
				zap.Stringer("provider", provider.DID()),
			)

			allocArgs := blobcaps.AllocateArguments{}
			err = datamodel.Rebind(datamodel.NewAny(allocInv.Arguments()), &allocArgs)
			if err != nil {
				log.Error("failed to rebind allocate arguments", zap.Error(err))
				return fmt.Errorf("rebinding allocate arguments: %w", err)
			}
			log = log.With(zap.String("digest", digestutil.Format(allocArgs.Blob.Digest)))

			info, err := router.GetProviderInfo(ctx, provider)
			if err != nil {
				log.Error("failed to get storage provider info", zap.Error(err))
				return fmt.Errorf("getting storage provider info: %w", err)
			}

			client, err := nodeProvider.Client(info.ID, info.Endpoint)
			if err != nil {
				log.Error("failed to create piri node", zap.Error(err))
				return fmt.Errorf("creating client: %w", err)
			}

			proofStore := ucan_server.NewContainerProofStore(meta)
			res, accInv, accRcpt, err := client.Accept(ctx, &piriclient.AcceptRequest{
				Space:  space.DID(),
				Digest: allocArgs.Blob.Digest,
				Size:   allocArgs.Blob.Size,
				Put:    putInv.Link(),
			}, proofStore)
			if err != nil {
				log.Error("failed to execute blob accept", zap.Error(err))
				return fmt.Errorf("executing blob accept: %w", err)
			}
			log = log.With(zap.Stringer("site", res.Site))

			err = writeAgentMessage(ctx, agentStore, []ucan.Invocation{accInv}, []ucan.Receipt{accRcpt})
			if err != nil {
				log.Error("failed to write agent message", zap.Error(err))
				return fmt.Errorf("writing agent message: %w", err)
			}

			// if accept task was not successful do not register the blob in the space
			return result.MatchResultR1(
				accRcpt.Out(),
				func(o ipld.Any) error {
					log.Debug("accept success")
					err := blobRegistry.Register(ctx, space.DID(), allocArgs.Blob, allocArgs.Cause)
					// it's ok if there's already a registration of this blob in this space
					if err != nil && !errors.Is(err, blobregistry.ErrEntryExists) {
						return err
					}
					return nil
				},
				func(x ipld.Any) error {
					var model edm.ErrorModel
					err := datamodel.Rebind(datamodel.NewAny(x), &model)
					if err != nil {
						log.Error("failed to bind execution failure", zap.Error(err))
						log.Error("failed execution", zap.Any("error", x))
						return fmt.Errorf("executing blob accept: %v", x)
					}
					log.Error("failed execution", zap.String("name", model.ErrorName), zap.Error(model))
					return fmt.Errorf("executing blob accept: %w", model)
				},
			)
		},
	}
}
