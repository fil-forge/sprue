package handlers

import (
	"fmt"

	"github.com/fil-forge/libforge/commands/access"
	"github.com/fil-forge/sprue/pkg/provisioning"
	delegation_store "github.com/fil-forge/sprue/pkg/store/delegation"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/ipfs/go-cid"
	"go.uber.org/zap"
)

func NewAccessDelegateHandler(delegationStore delegation_store.Store, provisioningSvc *provisioning.Service, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", access.Delegate))
	return server.NewRoute(
		access.Delegate,
		func(req *binding.Request[*access.DelegateArguments], res *binding.Response[*access.DelegateOK]) error {
			args := req.Task().Arguments()
			agent := req.Invocation().Issuer()
			space := req.Invocation().Subject()

			log := log.With(
				zap.Stringer("agent", agent),
				zap.Stringer("space", space),
			)
			log.Debug("delegating access", zap.Stringer("agent", agent))

			providers, err := provisioningSvc.ListServiceProviders(req.Context(), space)
			if err != nil {
				log.Error("failed to list service providers", zap.Error(err))
				return fmt.Errorf("listing service providers: %w", err)
			}
			if len(providers) == 0 {
				return res.SetFailure(errors.New(access.InsufficientStorageErrorName, "space has no storage provider"))
			}

			dlgs, err := extractDelegations(args, req.Metadata())
			if err != nil {
				log.Error("failed to extract delegations", zap.Error(err))
				return err
			}

			err = delegationStore.PutMany(req.Context(), dlgs, req.Invocation().Task().Link())
			if err != nil {
				log.Error("failed to store delegations", zap.Error(err))
				return err
			}

			return res.SetSuccess(&access.DelegateOK{})
		},
	)
}

func extractDelegations(args *access.DelegateArguments, meta ucan.Container) ([]ucan.Token, error) {
	all := make(map[cid.Cid]ucan.Token, len(args.Delegations))
	if meta != nil {
		for _, d := range meta.Delegations() {
			all[d.Link()] = d
		}
	}
	dlgs := make([]ucan.Token, 0, len(args.Delegations))
	for _, link := range args.Delegations {
		d, ok := all[link]
		if !ok {
			return nil, errors.New(access.DelegationNotFoundErrorName, "delegation not found: %s", link.String())
		}
		dlgs = append(dlgs, d)
	}
	return dlgs, nil
}
