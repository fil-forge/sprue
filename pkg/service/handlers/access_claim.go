package handlers

import (
	"fmt"

	"github.com/fil-forge/libforge/commands/access"
	"github.com/fil-forge/libforge/identity"
	delegation_store "github.com/fil-forge/sprue/pkg/store/delegation"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/ipfs/go-cid"
	"go.uber.org/zap"
)

func NewAccessClaimHandler(id identity.Identity, delegationStore delegation_store.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", access.Claim))
	return access.Claim.Route(
		func(req *binding.Request[*access.ClaimArguments], res *binding.Response[*access.ClaimOK]) error {
			agent := req.Invocation().Issuer()
			audience := req.Invocation().Subject()

			log := log.With(
				zap.Stringer("agent", agent),
				zap.Stringer("audience", audience),
			)
			log.Debug("claiming delegations")

			links := []cid.Cid{}
			delegations := []ucan.Delegation{}
			attestations := []ucan.Invocation{}
			var cursor *string
			for {
				var opts []delegation_store.ListByAudienceOption
				if cursor != nil {
					opts = append(opts, delegation_store.WithListByAudienceCursor(*cursor))
				}
				page, err := delegationStore.ListByAudience(req.Context(), audience, opts...)
				if err != nil {
					return fmt.Errorf("listing delegations: %w", err)
				}
				for _, token := range page.Results {
					if dlg, ok := token.(ucan.Delegation); ok {
						delegations = append(delegations, dlg)
						links = append(links, dlg.Link())
					} else if inv, ok := token.(ucan.Invocation); ok {
						attestations = append(attestations, inv)
					} else {
						log.Warn("unexpected token type in delegation store", zap.Stringer("link", token.Link()))
					}
				}
				if page.Cursor == nil {
					break
				}
				cursor = page.Cursor
			}

			res.SetMetadata(container.New(
				container.WithDelegations(delegations...),
				container.WithInvocations(attestations...),
			))

			return res.SetSuccess(&access.ClaimOK{Delegations: links})
		},
	)
}
