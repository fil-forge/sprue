package handlers

import (
	stderrors "errors"
	"fmt"

	routingcmds "github.com/fil-forge/libforge/commands/routing"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"go.uber.org/zap"
)

// NewRoutingPutHandler replaces the candidate set of the routing policy that is
// the invocation subject. The dispatcher has already verified the issuer holds
// a proof chain rooted at the policy, so the handler only validates the set.
func NewRoutingPutHandler(router *routing.Service, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", routingcmds.Put))
	return routingcmds.Put.Route(
		func(req *binding.Request[*routingcmds.PutArguments], res *binding.Response[*routingcmds.PutOK]) error {
			policy := req.Invocation().Subject()
			args := req.Task().Arguments()
			log := log.With(zap.Stringer("policy", policy), zap.Int("candidates", len(args.Candidates.Entries)))
			log.Debug("putting routing policy")

			candidates := make([]did.DID, 0, len(args.Candidates.Entries))
			for c := range args.Candidates.Entries {
				candidates = append(candidates, c)
			}

			err := router.PutPolicy(req.Context(), policy, candidates, req.Invocation().Task().Link())
			if err != nil {
				var named errors.Named
				if stderrors.As(err, &named) && named.Name() == routing.InvalidCandidatesErrorName {
					log.Warn("invalid routing policy candidates", zap.Error(err))
					return res.SetFailure(err)
				}
				log.Error("failed to put routing policy", zap.Error(err))
				return fmt.Errorf("putting routing policy: %w", err)
			}

			return res.SetSuccess(&routingcmds.PutOK{})
		},
	)
}
