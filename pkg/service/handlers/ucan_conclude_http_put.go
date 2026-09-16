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
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/ipfs/go-cid"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// allocationLookupConcurrency bounds the parallel agent-store reads that
// resolve each concluded put to its allocation. On the postgres backend every
// read is an object-store GET, so a batch of them is worth overlapping; the
// limit keeps a large batch from swamping the store.
const allocationLookupConcurrency = 16

// concludedPut is one delivered /http/put receipt resolved to the allocation
// it fulfils — the space, blob and provider its acceptance needs.
type concludedPut struct {
	putInv    ucan.Invocation
	provider  did.DID
	space     did.DID
	blob      blobcmds.Blob
	cause     cid.Cid
	acceptReq *piriclient.AcceptRequest
}

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
		Handler: func(ctx context.Context, conclusions []Conclusion) (ucan.Container, error) {
			if len(conclusions) == 0 {
				return nil, nil
			}
			log := log.With(zap.Int("puts", len(conclusions)))
			log.Debug("handling conclude")

			puts, err := resolveAllocations(ctx, agentStore, conclusions, log)
			if err != nil {
				return nil, err
			}

			// One request per provider rather than per blob: the accepts of a
			// batch usually land on very few nodes.
			var providers []did.DID
			byProvider := map[did.DID][]*concludedPut{}
			for _, put := range puts {
				if _, seen := byProvider[put.provider]; !seen {
					providers = append(providers, put.provider)
				}
				byProvider[put.provider] = append(byProvider[put.provider], put)
			}

			var invocations []ucan.Invocation
			var receipts []ucan.Receipt
			for _, provider := range providers {
				invs, rcpts, err := acceptOnProvider(
					ctx, router, nodeProvider, agentStore, blobRegistry,
					provider, byProvider[provider], log)
				invocations = append(invocations, invs...)
				receipts = append(receipts, rcpts...)
				if err != nil {
					return container.New(
						container.WithInvocations(invocations...),
						container.WithReceipts(receipts...),
					), err
				}
			}
			return container.New(
				container.WithInvocations(invocations...),
				container.WithReceipts(receipts...),
			), nil
		},
	}
}

// resolveAllocations turns each delivered put receipt into the allocation it
// fulfils. The allocate invocation names the provider (its subject) and
// carries the space, blob and cause; without it there is no acceptance to
// make, so a failure here fails the conclusion.
func resolveAllocations(ctx context.Context, agentStore agent.Store, conclusions []Conclusion, log *zap.Logger) ([]*concludedPut, error) {
	puts := make([]*concludedPut, len(conclusions))
	grp, ctx := errgroup.WithContext(ctx)
	grp.SetLimit(allocationLookupConcurrency)
	for i, conclusion := range conclusions {
		grp.Go(func() error {
			log := log.With(zap.Stringer("ran", conclusion.Receipt.Ran()))

			var putArgs httpcmds.PutArguments
			if err := putArgs.UnmarshalCBOR(bytes.NewReader(conclusion.Invocation.ArgumentsBytes())); err != nil {
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

			var allocArgs blobcmds.AllocateArguments
			if err := allocArgs.UnmarshalCBOR(bytes.NewReader(allocInv.ArgumentsBytes())); err != nil {
				log.Error("failed to unmarshal allocate arguments", zap.Error(err))
				return fmt.Errorf("unmarshaling allocate arguments: %w", err)
			}

			puts[i] = &concludedPut{
				putInv: conclusion.Invocation,
				// The allocate invocation's subject and audience are both the
				// storage provider (its proofs are rooted at the provider).
				// The space travels in the allocate arguments rather than on
				// the subject.
				provider: allocInv.Subject(),
				space:    allocArgs.Space,
				blob:     allocArgs.Blob,
				cause:    allocArgs.Cause,
				acceptReq: &piriclient.AcceptRequest{
					Space:  allocArgs.Space,
					Digest: allocArgs.Blob.Digest,
					Size:   allocArgs.Blob.Size,
					Put:    conclusion.Invocation.Task().Link(),
				},
			}
			return nil
		})
	}
	if err := grp.Wait(); err != nil {
		return nil, err
	}
	return puts, nil
}

// acceptOnProvider accepts every concluded put that landed on one provider,
// persists the results, and registers the blobs that were accepted. It
// returns the accept invocations and receipts (with the extras the node
// attached — the location commitments and PDP promises) for the conclude
// response, so a deliverer reads each blob's outcome from the answer rather
// than polling for it.
//
// A blob whose acceptance failed is reported by its own failure receipt and
// is not registered; that is not an error for the rest of the batch. An error
// means the provider could not be reached at all.
func acceptOnProvider(
	ctx context.Context,
	router *routing.Service,
	nodeProvider piriclient.Provider,
	agentStore agent.Store,
	blobRegistry blobregistry.Store,
	provider did.DID,
	puts []*concludedPut,
	log *zap.Logger,
) ([]ucan.Invocation, []ucan.Receipt, error) {
	log = log.With(zap.Stringer("provider", provider), zap.Int("blobs", len(puts)))

	info, err := router.GetProviderInfo(ctx, provider)
	if err != nil {
		log.Error("failed to get storage provider info", zap.Error(err))
		return nil, nil, fmt.Errorf("getting storage provider info: %w", err)
	}
	client, err := nodeProvider.Client(info.ID, info.Endpoint)
	if err != nil {
		log.Error("failed to create piri node", zap.Error(err))
		return nil, nil, fmt.Errorf("creating client: %w", err)
	}
	proofStore := ucanlib.NewContainerProofStore(info.Proofs)

	reqs := make([]*piriclient.AcceptRequest, len(puts))
	for i, put := range puts {
		reqs[i] = put.acceptReq
	}
	// Must match the accInv constructed in blob_add.go maybeAccept:
	// (1) Put = putInv.Task().Link() and
	// (2) WithNoNonce, so this invocation's CID matches the one whose
	// task link was returned to the client as AddOK.Site.Task and is
	// what the client polls the receipts endpoint for. A divergence
	// here means the receipt is stored under a CID nobody polls for,
	// producing "receipt not found after N attempts" client-side.
	results, metas, err := client.AcceptBatch(ctx, reqs, proofStore, invocation.WithNoNonce())
	if err != nil {
		log.Error("failed to execute blob accept", zap.Error(err))
		return nil, nil, fmt.Errorf("executing blob accept: %w", err)
	}

	// The accept receipts and the artifacts piri attached to them (location
	// commitments, PDP promises) are persisted in one agent message, which is
	// what makes them retrievable by task link from the receipts endpoint.
	var accInvs []ucan.Invocation
	var accRcpts []ucan.Receipt
	for _, res := range results {
		accInvs = append(accInvs, res.Invocation)
		if res.Receipt != nil {
			accRcpts = append(accRcpts, res.Receipt)
		}
	}
	for _, meta := range metas {
		if meta == nil {
			continue
		}
		accInvs = append(accInvs, meta.Invocations()...)
		accRcpts = append(accRcpts, meta.Receipts()...)
	}
	if err := writeAgentMessage(ctx, agentStore, accInvs, accRcpts); err != nil {
		log.Error("failed to write agent message", zap.Error(err))
		return accInvs, accRcpts, fmt.Errorf("writing agent message: %w", err)
	}

	for i, res := range results {
		put := puts[i]
		log := log.With(
			zap.Stringer("space", put.space),
			zap.String("digest", digestutil.Format(put.blob.Digest)),
			zap.Stringer("accept", res.Invocation.Task().Link()),
		)
		if res.Receipt == nil {
			// The node answered the request but not this invocation. Nothing
			// attests the blob was accepted, so it is not registered; the
			// deliverer sees a missing receipt and can retry.
			log.Error("blob accept returned no receipt")
			continue
		}
		if res.Receipt.Out().IsErr() {
			_, x := res.Receipt.Out().Unpack()
			var model edm.ErrorModel
			if err := model.UnmarshalCBOR(bytes.NewReader(x)); err != nil {
				log.Error("failed to unmarshal blob accept execution failure", zap.Error(err), zap.Binary("input", x))
				continue
			}
			log.Error("failed execution of blob accept", zap.String("name", model.ErrorName), zap.Error(model))
			continue
		}
		log.Debug("accept success")
		err := blobRegistry.Register(ctx, put.space, put.blob, put.cause)
		// it's ok if there's already a registration of this blob in this space
		if err != nil && !errors.Is(err, blobregistry.ErrEntryExists) {
			return accInvs, accRcpts, err
		}
	}
	return accInvs, accRcpts, nil
}
