package handlers

import (
	"bytes"
	"context"
	"fmt"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/libforge/digestutil"
	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/store/agent"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	"github.com/fil-forge/sprue/pkg/store/replica"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/ipfs/go-cid"
	"go.uber.org/zap"
)

// NewBlobRemoveHandler removes a blob from a space: it deregisters the blob
// and forwards /blob/remove to every storage node holding it (the primary
// provider recovered via the registration's receipt chain, plus replicas).
//
// Forwarding is best-effort: a node that cannot be reached is logged and
// skipped rather than failing the removal — piri's handler is idempotent and
// unclaimed allocations expire, so stragglers are reconciled by provider-side
// hygiene. Removing an unregistered blob is idempotent success.
func NewBlobRemoveHandler(router *routing.Service, nodeProvider piriclient.Provider, agentStore agent.Store, blobRegistry blobregistry.Store, replicaStore replica.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", blobcmds.Remove))
	return blobcmds.Remove.Route(
		func(req *binding.Request[*blobcmds.RemoveArguments], res *binding.Response[*blobcmds.RemoveOK]) error {
			args := req.Task().Arguments()
			space := req.Invocation().Subject()
			cause := req.Invocation().Task().Link()
			log := log.With(
				zap.Stringer("space", space),
				zap.String("blob", digestutil.Format(args.Digest)),
			)
			log.Debug("removing blob")

			reg, err := blobRegistry.Get(req.Context(), space, args.Digest)
			if err != nil {
				if errors.Is(err, blobregistry.ErrEntryNotFound) {
					log.Debug("blob not registered in space, nothing to remove")
					return res.SetSuccess(&blobcmds.RemoveOK{})
				}
				log.Error("failed to get blob registration", zap.Error(err))
				return fmt.Errorf("getting blob registration: %w", err)
			}

			// Collect every provider holding the blob for this space: the
			// primary (recovered from the registration's receipt chain) and
			// any replicas.
			providers := map[did.DID]struct{}{}
			primary, err := primaryProviderForBlob(req.Context(), agentStore, reg.Cause)
			if err != nil {
				log.Warn("failed to recover primary provider from receipt chain", zap.Error(err))
			} else {
				providers[primary] = struct{}{}
			}
			replicas, err := replicaStore.List(req.Context(), space, args.Digest)
			if err != nil {
				log.Warn("failed to list replicas", zap.Error(err))
			}
			for _, r := range replicas {
				if r.Status == replica.Failed {
					continue
				}
				providers[r.Provider] = struct{}{}
			}

			// Forward the removal to each provider (best-effort), then
			// deregister. Deregistering last keeps the registration — and
			// with it the receipt chain to the primary — available for a
			// retry if every forward fails.
			for provider := range providers {
				if err := forwardBlobRemove(req.Context(), router, nodeProvider, agentStore, provider, space, args.Digest); err != nil {
					log.Warn("failed to forward blob removal to provider",
						zap.Stringer("provider", provider), zap.Error(err))
				}
			}

			if err := blobRegistry.Deregister(req.Context(), space, args.Digest, cause); err != nil {
				log.Error("failed to deregister blob", zap.Error(err))
				return fmt.Errorf("deregistering blob: %w", err)
			}

			return res.SetSuccess(&blobcmds.RemoveOK{})
		},
	)
}

// primaryProviderForBlob recovers the storage provider that holds the primary
// copy of a registered blob by walking the registration's receipt chain: the
// registration cause is the /space/blob/add task, whose receipt's Site
// promise points at the /blob/accept invocation, whose subject is the
// provider. (Mirrors the chain walk in NewBlobAddHandler's already-registered
// branch.)
func primaryProviderForBlob(ctx context.Context, agentStore agent.Store, cause cid.Cid) (did.DID, error) {
	addRcpt, err := agentStore.GetReceipt(ctx, cause)
	if err != nil {
		return did.Undef, fmt.Errorf("getting receipt for blob registration: %w", err)
	}
	if addRcpt.Out().IsErr() {
		return did.Undef, fmt.Errorf("blob registration receipt contains failure")
	}

	o, _ := addRcpt.Out().Unpack()
	var addOK blobcmds.AddOK
	if err := addOK.UnmarshalCBOR(bytes.NewReader(o)); err != nil {
		return did.Undef, fmt.Errorf("unmarshaling add OK result: %w", err)
	}

	accInv, err := agentStore.GetInvocation(ctx, addOK.Site.Task)
	if err != nil {
		return did.Undef, fmt.Errorf("getting invocation for blob accept: %w", err)
	}
	return accInv.Subject(), nil
}

// forwardBlobRemove sends /blob/remove {space, digest} to a single provider
// and records the exchanged invocation + receipt in the agent store.
func forwardBlobRemove(
	ctx context.Context,
	router *routing.Service,
	nodeProvider piriclient.Provider,
	agentStore agent.Store,
	provider did.DID,
	space did.DID,
	digest []byte,
) error {
	info, err := router.GetProviderInfo(ctx, provider)
	if err != nil {
		return fmt.Errorf("getting provider info: %w", err)
	}
	client, err := nodeProvider.Client(info.ID, info.Endpoint)
	if err != nil {
		return fmt.Errorf("creating piri client: %w", err)
	}

	// The proof chain for /blob/remove comes from the proofs the provider
	// granted the upload service at registration.
	proofStore := ucanlib.NewContainerProofStore(info.Proofs)
	_, inv, rcpt, err := client.Remove(ctx, &piriclient.RemoveRequest{
		Space:  space,
		Digest: digest,
	}, proofStore)
	if err != nil {
		return fmt.Errorf("executing remove on provider: %w", err)
	}

	if err := writeAgentMessage(ctx, agentStore, []ucan.Invocation{inv}, []ucan.Receipt{rcpt}); err != nil {
		return fmt.Errorf("writing agent message: %w", err)
	}
	return nil
}
