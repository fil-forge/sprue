package handlers

import (
	"context"
	"fmt"

	"github.com/fil-forge/go-libstoracha/capabilities/blob"
	"github.com/fil-forge/go-libstoracha/capabilities/http"
	"github.com/fil-forge/go-libstoracha/capabilities/ucan"
	"github.com/fil-forge/go-libstoracha/digestutil"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure/datamodel"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/validator"
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/store/agent"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
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
		zap.String("handler", ucan.ConcludeAbility),
		zap.String("conclude", http.PutAbility),
	)
	return ConclusionHandler{
		Ability: http.PutAbility,
		Handler: func(ctx context.Context, putInv invocation.Invocation, putRcpt receipt.AnyReceipt, _ server.InvocationContext) error {
			log := log.With(zap.Stringer("ran", putRcpt.Ran().Link()))
			log.Debug("handling conclude")

			var err error
			putCap := putInv.Capabilities()[0]
			putMatch, err := http.Put.Match(validator.NewSource(putCap, putInv))
			if err != nil {
				log.Error("failed to match http/put invocation", zap.Error(err))
				return fmt.Errorf("matching http/put invocation: %w", err)
			}

			allocateTaskLink, err := ipldutil.ToCID(putMatch.Value().Nb().URL.UcanAwait.Link)
			if err != nil {
				return err
			}
			log = log.With(zap.Stringer("allocation", allocateTaskLink))

			allocTask, err := agentStore.GetInvocation(ctx, allocateTaskLink)
			if err != nil {
				log.Error("failed to get allocation invocation", zap.Error(err))
				return fmt.Errorf("getting allocation invocation: %w", err)
			}
			log = log.With(zap.Stringer("provider", allocTask.Audience().DID()))

			allocMatch, err := blob.Allocate.Match(validator.NewSource(allocTask.Capabilities()[0], allocTask))
			if err != nil {
				log.Error("matching blob/allocate invocation", zap.Error(err))
				return fmt.Errorf("matching blob/allocate invocation: %w", err)
			}

			allocNb := allocMatch.Value().Nb()
			log = log.With(
				zap.Stringer("space", allocNb.Space),
				zap.String("digest", digestutil.Format(allocNb.Blob.Digest)),
			)

			info, err := router.GetProviderInfo(ctx, allocTask.Audience())
			if err != nil {
				log.Error("failed to get storage provider info", zap.Error(err))
				return fmt.Errorf("getting storage provider info: %w", err)
			}

			client, err := nodeProvider.Client(info.ID, info.Endpoint)
			if err != nil {
				log.Error("failed to create piri node", zap.Error(err))
				return fmt.Errorf("creating client: %w", err)
			}

			res, accTask, accRcpt, err := client.Accept(ctx, &piriclient.AcceptRequest{
				Space:  allocNb.Space,
				Digest: allocNb.Blob.Digest,
				Size:   allocNb.Blob.Size,
				Put:    putInv.Link(),
			}, delegationFetcher{info.Proof})
			if err != nil {
				log.Error("failed to execute blob/accept", zap.Error(err))
				return fmt.Errorf("executing blob/accept: %w", err)
			}
			log = log.With(zap.Stringer("site", res.Site))

			err = writeAgentMessage(ctx, agentStore, []invocation.Invocation{accTask}, []receipt.AnyReceipt{accRcpt})
			if err != nil {
				log.Error("failed to write agent message", zap.Error(err))
				return fmt.Errorf("writing agent message: %w", err)
			}

			// if accept task was not successful do not register the blob in the space
			return result.MatchResultR1(
				accRcpt.Out(),
				func(o ipld.Node) error {
					log.Debug("accept success")
					err := blobRegistry.Register(ctx, allocNb.Space, allocNb.Blob, allocateTaskLink)
					// it's ok if there's already a registration of this blob in this space
					if err != nil && !errors.Is(err, blobregistry.ErrEntryExists) {
						return err
					}
					return nil
				},
				func(x ipld.Node) error {
					f := datamodel.Bind(x)
					log.Error("failed blob/accept receipt", zap.Error(f))
					return f
				},
			)
		},
	}
}
