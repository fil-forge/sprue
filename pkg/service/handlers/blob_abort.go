package handlers

import (
	"fmt"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/libforge/digestutil"
	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"go.uber.org/zap"
)

// MissingCauseErrorName is the stable receipt-failure name when a
// /blob/abort invocation carries no Cause — without the /space/blob/add
// task link there is no way to recover which storage node holds the parked
// blob (it has no registration or acceptance to look up by).
const MissingCauseErrorName = "MissingCause"

// NewBlobAbortHandler abandons a space's in-flight upload of a parked
// (never-accepted) blob: it recovers the storage node holding it from the
// Cause receipt chain and forwards a /blob/reject there. Nothing is
// deregistered — registration happens only at accept, which a parked blob
// never reached.
//
// Forward errors are propagated as receipt failures: abort mutates no
// local state, so the caller can simply retry.
func NewBlobAbortHandler(router *routing.Service, nodeProvider piriclient.Provider, agentStore agent.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", blobcmds.Abort))
	return blobcmds.Abort.Route(
		func(req *binding.Request[*blobcmds.AbortArguments], res *binding.Response[*blobcmds.AbortOK]) error {
			args := req.Task().Arguments()
			space := req.Invocation().Subject()
			log := log.With(
				zap.Stringer("space", space),
				zap.String("blob", digestutil.Format(args.Digest)),
			)
			log.Debug("aborting blob upload")

			if !args.Cause.Defined() {
				return res.SetFailure(errors.New(MissingCauseErrorName,
					"abort requires the /space/blob/add task link (cause) to locate the provider"))
			}

			provider, err := primaryProviderForBlob(req.Context(), agentStore, args.Cause)
			if err != nil {
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
			_, inv, rcpt, err := client.Reject(req.Context(), &piriclient.RejectRequest{
				Space:  space,
				Digest: args.Digest,
			}, proofStore)
			if err != nil {
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
