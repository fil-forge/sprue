package handlers

import (
	"bytes"
	"context"
	"fmt"

	blobcaps "github.com/fil-forge/libforge/commands/blob"
	httpcaps "github.com/fil-forge/libforge/commands/http"
	ucancaps "github.com/fil-forge/libforge/commands/ucan"
	"github.com/fil-forge/libforge/digestutil"
	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/store/agent"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	"github.com/fil-forge/ucantone/errors"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
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
		zap.String("handler", string(ucancaps.Conclude)),
		zap.String("conclude", string(httpcaps.Put)),
	)
	return ConclusionHandler{
		Command: ucan.Command(httpcaps.Put),
		Handler: func(ctx context.Context, putInv ucan.Invocation, putRcpt ucan.Receipt, meta ucan.Container) error {
			log := log.With(zap.Stringer("ran", putRcpt.Ran()))
			log.Debug("handling conclude")

			var putArgs httpcaps.PutArguments
			if err := putArgs.UnmarshalCBOR(bytes.NewReader(putInv.ArgumentsBytes())); err != nil {
				log.Error("failed to unmarshal HTTP PUT arguments", zap.Error(err))
				return fmt.Errorf("unmarshaling HTTP PUT arguments: %w", err)
			}

			allocTaskLink := putArgs.Destination.Task
			log = log.With(zap.Stringer("allocation", allocTaskLink))

			allocInv, err := agentStore.GetInvocation(ctx, allocTaskLink)
			if err != nil {
				log.Error("failed to get allocation invocation", zap.Error(err))
				return fmt.Errorf("getting allocation invocation: %w", err)
			}

			provider := allocInv.Audience()
			if !provider.Defined() {
				// shouldn't happen, subject should be the space and audience the node
				provider = allocInv.Subject()
			}
			space := allocInv.Subject()

			log = log.With(
				zap.Stringer("space", space),
				zap.Stringer("provider", provider),
			)

			var allocArgs blobcaps.AllocateArguments
			if err := allocArgs.UnmarshalCBOR(bytes.NewReader(allocInv.ArgumentsBytes())); err != nil {
				log.Error("failed to unmarshal allocate arguments", zap.Error(err))
				return fmt.Errorf("unmarshaling allocate arguments: %w", err)
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

			proofStore := ucanlib.NewContainerProofStore(meta)
			res, accInv, accRcpt, err := client.Accept(ctx, &piriclient.AcceptRequest{
				Space:  space,
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

			if accRcpt.Out().IsErr() {
				_, x := accRcpt.Out().Unpack()
				var model edm.ErrorModel
				if err := model.UnmarshalCBOR(bytes.NewReader(x)); err != nil {
					log.Error("failed to unmarshal blob accept execution failure", zap.Error(err), zap.Binary("input", x))
					return fmt.Errorf("executing blob accept: %v", x)
				}
				log.Error("failed execution of blob accept", zap.String("name", model.ErrorName), zap.Error(model))
				return fmt.Errorf("executing blob accept: %w", model)
			}

			log.Debug("accept success")
			err = blobRegistry.Register(ctx, space, allocArgs.Blob, allocArgs.Cause)
			// it's ok if there's already a registration of this blob in this space
			if err != nil && !errors.Is(err, blobregistry.ErrEntryExists) {
				return err
			}
			return nil
		},
	}
}
