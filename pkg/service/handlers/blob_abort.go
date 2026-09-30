package handlers

import (
	"bytes"
	"context"
	"fmt"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	httpcmds "github.com/fil-forge/libforge/commands/http"
	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/ipfs/go-cid"
	"go.uber.org/zap"
)

// NewBlobAbortHandler abandons a space's in-flight upload of a parked
// (never-accepted) blob: it recovers the storage node holding it from the
// Cause receipt chain and forwards a /blob/reject there. The reject names the
// blob as the upload did: by digest, or, for a blob added without one, by its
// allocation, so the node also drops the upload it is still expecting. Nothing is
// deregistered — registration happens only at accept, which a parked blob
// never reached.
//
// A cause that does not resolve to a known /blob/add task fails with
// the named error MissingCause; a node refusing the translated reject
// because the space has accepted the blob surfaces the node's BlobAccepted
// as a named failure, so the client can distinguish "use /blob/remove"
// from a retryable fault. Other forward errors are propagated as generic
// receipt failures: abort mutates no local state, so the caller can simply
// retry.
func NewBlobAbortHandler(router *routing.Service, nodeProvider piriclient.Provider, agentStore agent.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", blobcmds.Abort))
	return blobcmds.Abort.Route(
		func(req *binding.Request[*blobcmds.AbortArguments], res *binding.Response[*blobcmds.AbortOK]) error {
			args := req.Task().Arguments()
			space := req.Invocation().Subject()
			log := log.With(zap.Stringer("space", space), zap.Stringer("cause", args.Cause))
			log.Debug("aborting blob upload")

			if !args.Cause.Defined() {
				return res.SetFailure(blobcmds.ErrMissingCause)
			}

			provider, reject, err := rejectionFor(req.Context(), agentStore, space, args.Cause)
			if err != nil {
				// An unknown cause — one whose receipt chain we don't hold —
				// cannot route to a node; per the RFC it is the named error
				// MissingCause, not a retryable execution fault.
				if errors.Is(err, agent.ErrReceiptNotFound) || errors.Is(err, agent.ErrInvocationNotFound) {
					log.Debug("cause does not resolve to a known blob add task", zap.Error(err))
					return res.SetFailure(errors.New(blobcmds.MissingCauseErrorName,
						"cause does not resolve to a known /blob/add task"))
				}
				log.Error("failed to recover provider from receipt chain", zap.Error(err))
				return fmt.Errorf("recovering provider for parked blob: %w", err)
			}

			info, err := router.GetProviderInfo(req.Context(), provider)
			if err != nil {
				log.Error("failed to get provider info", zap.Error(err))
				return fmt.Errorf("getting provider info: %w", err)
			}
			client, err := nodeProvider.Client(info.ID, info.Endpoint)
			if err != nil {
				log.Error("failed to create piri client", zap.Error(err))
				return fmt.Errorf("creating piri client: %w", err)
			}

			// The proof chain for /blob/reject comes from the proofs the
			// provider granted the upload service at registration.
			proofStore := ucanlib.NewContainerProofStore(info.Proofs)
			_, inv, rcpt, err := client.Reject(req.Context(), &reject, proofStore)
			if err != nil {
				// The node refuses to reject a blob this space has accepted.
				// Surface the named failure rather than a generic fault so
				// the client knows to use /blob/remove instead of retrying.
				var named errors.Named
				if errors.As(err, &named) && named.Name() == blobcmds.BlobAcceptedErrorName {
					log.Debug("provider refused reject: blob accepted by space",
						zap.Stringer("provider", provider))
					return res.SetFailure(named)
				}
				log.Error("failed to execute reject on provider",
					zap.Stringer("provider", provider), zap.Error(err))
				return fmt.Errorf("executing reject on provider: %w", err)
			}

			if err := writeAgentMessage(req.Context(), agentStore, []ucan.Invocation{inv}, []ucan.Receipt{rcpt}); err != nil {
				log.Error("failed to write agent message", zap.Error(err))
				return fmt.Errorf("writing agent message: %w", err)
			}

			return res.SetSuccess(&blobcmds.AbortOK{})
		},
	)
}

// rejectionFor recovers, from the receipt chain of the /blob/add task cause,
// the storage node an upload went to and the /blob/reject that retires it
// there. An accept that names the digest rejects by digest; one that names
// only the hash function rejects the allocation its /http/put was made to.
func rejectionFor(ctx context.Context, agentStore agent.Store, space did.DID, cause cid.Cid) (did.DID, blobcmds.RejectArguments, error) {
	addRcpt, err := agentStore.GetReceipt(ctx, cause)
	if err != nil {
		return did.Undef, blobcmds.RejectArguments{}, fmt.Errorf("getting receipt for blob add: %w", err)
	}
	if addRcpt.Out().IsErr() {
		return did.Undef, blobcmds.RejectArguments{}, fmt.Errorf("blob add receipt contains failure")
	}
	o, _ := addRcpt.Out().Unpack()
	var addOK blobcmds.AddOK
	if err := addOK.UnmarshalCBOR(bytes.NewReader(o)); err != nil {
		return did.Undef, blobcmds.RejectArguments{}, fmt.Errorf("unmarshaling add OK result: %w", err)
	}

	accInv, err := agentStore.GetInvocation(ctx, addOK.Site.Task)
	if err != nil {
		return did.Undef, blobcmds.RejectArguments{}, fmt.Errorf("getting invocation for blob accept: %w", err)
	}
	var accArgs blobcmds.AcceptArguments
	if err := accArgs.UnmarshalCBOR(bytes.NewReader(accInv.ArgumentsBytes())); err != nil {
		return did.Undef, blobcmds.RejectArguments{}, fmt.Errorf("unmarshaling accept arguments: %w", err)
	}
	provider := accInv.Subject()
	if b, ok := accArgs.Blob.Blob(); ok {
		return provider, blobcmds.RejectByDigest(space, b.Digest), nil
	}

	putInv, err := agentStore.GetInvocation(ctx, accArgs.Put.Task)
	if err != nil {
		return did.Undef, blobcmds.RejectArguments{}, fmt.Errorf("getting invocation for HTTP PUT: %w", err)
	}
	var putArgs httpcmds.PutArguments
	if err := putArgs.UnmarshalCBOR(bytes.NewReader(putInv.ArgumentsBytes())); err != nil {
		return did.Undef, blobcmds.RejectArguments{}, fmt.Errorf("unmarshaling HTTP PUT arguments: %w", err)
	}
	return provider, blobcmds.RejectByAllocation(space, putArgs.Destination.Task), nil
}
