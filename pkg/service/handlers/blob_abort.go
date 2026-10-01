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
// receipt chain of the Add task and forwards a /blob/reject of the allocation
// the add made there, which the node knows the space and blob of. Nothing is
// deregistered — registration happens only at accept, which a parked blob
// never reached.
//
// An add that does not resolve to a known /blob/add task in the space fails
// with the named error MissingCause; a node refusing the translated reject
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
			log := log.With(zap.Stringer("space", space), zap.Stringer("add", args.Add))
			log.Debug("aborting blob upload")

			if !args.Add.Defined() {
				return res.SetFailure(blobcmds.ErrMissingCause)
			}

			provider, reject, err := rejectionFor(req.Context(), agentStore, space, args.Add)
			if err != nil {
				// An unknown add — one whose receipt chain we don't hold, or
				// another space's — cannot route to a node; per the RFC it is
				// the named error MissingCause, not a retryable execution fault.
				if errors.Is(err, agent.ErrReceiptNotFound) || errors.Is(err, agent.ErrInvocationNotFound) || errors.Is(err, errOtherSpace) {
					log.Debug("add does not resolve to a known blob add task", zap.Error(err))
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

// errOtherSpace reports an add task that belongs to a space other than the
// abort's.
var errOtherSpace = errors.New("OtherSpace", "add task belongs to another space")

// rejectionFor recovers, from the receipt chain of the /blob/add task add, the
// storage node an upload went to and the /blob/reject that retires it there:
// a reject of the allocation its /http/put was made to. The reject names no
// space, so the add is checked to be space's here; another space's add fails
// with errOtherSpace.
func rejectionFor(ctx context.Context, agentStore agent.Store, space did.DID, add cid.Cid) (did.DID, blobcmds.RejectArguments, error) {
	addRcpt, err := agentStore.GetReceipt(ctx, add)
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
	if accArgs.Space != space {
		return did.Undef, blobcmds.RejectArguments{}, errOtherSpace
	}

	putInv, err := agentStore.GetInvocation(ctx, accArgs.Put.Task)
	if err != nil {
		return did.Undef, blobcmds.RejectArguments{}, fmt.Errorf("getting invocation for HTTP PUT: %w", err)
	}
	var putArgs httpcmds.PutArguments
	if err := putArgs.UnmarshalCBOR(bytes.NewReader(putInv.ArgumentsBytes())); err != nil {
		return did.Undef, blobcmds.RejectArguments{}, fmt.Errorf("unmarshaling HTTP PUT arguments: %w", err)
	}
	return accInv.Subject(), blobcmds.RejectArguments{Allocation: putArgs.Destination.Task}, nil
}
