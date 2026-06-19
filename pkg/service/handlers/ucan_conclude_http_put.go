package handlers

import (
	"bytes"
	"context"
	"fmt"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	httpcmds "github.com/fil-forge/libforge/commands/http"
	ucancmds "github.com/fil-forge/libforge/commands/ucan"
	"github.com/fil-forge/libforge/digestutil"
	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/store/agent"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	"github.com/fil-forge/ucantone/errors"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/invocation"
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
		zap.Stringer("handler", ucancmds.Conclude),
		zap.Stringer("conclude", httpcmds.Put),
	)
	return ConclusionHandler{
		Command: httpcmds.Put.Command,
		Handler: func(ctx context.Context, putInv ucan.Invocation, putRcpt ucan.Receipt) error {
			log := log.With(zap.Stringer("ran", putRcpt.Ran()))
			log.Debug("handling conclude")

			var putArgs httpcmds.PutArguments
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

			// The allocate invocation's subject and audience are both the storage
			// provider (its proofs are rooted at the provider). The space now
			// travels in the allocate arguments rather than on the subject.
			provider := allocInv.Subject()

			var allocArgs blobcmds.AllocateArguments
			if err := allocArgs.UnmarshalCBOR(bytes.NewReader(allocInv.ArgumentsBytes())); err != nil {
				log.Error("failed to unmarshal allocate arguments", zap.Error(err))
				return fmt.Errorf("unmarshaling allocate arguments: %w", err)
			}
			space := allocArgs.Space

			log = log.With(
				zap.Stringer("space", space),
				zap.Stringer("provider", provider),
				zap.String("digest", digestutil.Format(allocArgs.Blob.Digest)),
			)

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

			proofStore := ucanlib.NewContainerProofStore(info.Proofs)
			// Must match the accInv constructed in blob_add.go maybeAccept:
			// (1) Put = putInv.Task().Link() and
			// (2) WithNoNonce, so this invocation's CID matches the one whose
			// task link was returned to the client as AddOK.Site.Task and is
			// what the client polls the receipts endpoint for. A divergence
			// here means the receipt is stored under a CID nobody polls for,
			// producing "receipt not found after N attempts" client-side.
			res, accInv, accRcpt, meta, err := client.Accept(ctx, &piriclient.AcceptRequest{
				Space:  space,
				Digest: allocArgs.Blob.Digest,
				Size:   allocArgs.Blob.Size,
				Put:    putInv.Task().Link(),
			}, proofStore, invocation.WithNoNonce())
			if err != nil {
				log.Error("failed to execute blob accept", zap.Error(err))
				return fmt.Errorf("executing blob accept: %w", err)
			}
			log = log.With(zap.Stringer("site", res.Site))

			// accept invocation includes a location commitment (invocation) in response
			accInvs := []ucan.Invocation{accInv}
			accRcpts := []ucan.Receipt{accRcpt}
			if meta != nil {
				accInvs = append(accInvs, meta.Invocations()...)
				accRcpts = append(accRcpts, meta.Receipts()...)
			}

			err = writeAgentMessage(ctx, agentStore, accInvs, accRcpts)
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
