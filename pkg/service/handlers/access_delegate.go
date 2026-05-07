package handlers

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/fil-forge/go-libstoracha/capabilities/access"
	"github.com/fil-forge/go-ucanto/core/dag/blockstore"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/provisioning"
	delegation_store "github.com/fil-forge/sprue/pkg/store/delegation"
)

const (
	DelegationNotFoundErrorName  = "DelegationNotFound"
	InsufficientStorageErrorName = "InsufficientStorage"
)

// WithAccessDelegateMethod registers the access/delegate handler.
// This handler stores delegations for later retrieval.
func WithAccessDelegateMethod(delegationStore delegation_store.Store, provisioningSvc *provisioning.Service, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		access.DelegateAbility,
		server.Provide(
			access.Delegate,
			AccessDelegateHandler(delegationStore, provisioningSvc, logger),
		),
	)
}

func AccessDelegateHandler(delegationStore delegation_store.Store, provisioningSvc *provisioning.Service, logger *zap.Logger) server.HandlerFunc[access.DelegateCaveats, access.DelegateOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", access.DelegateAbility))
	return func(ctx context.Context,
		cap ucan.Capability[access.DelegateCaveats],
		inv invocation.Invocation,
		iCtx server.InvocationContext,
	) (result.Result[access.DelegateOk, failure.IPLDBuilderFailure], fx.Effects, error) {
		agent := inv.Issuer().DID()
		space, err := did.Parse(cap.With())
		if err != nil {
			log.Warn("invalid resource", zap.String("resource", cap.With()))
			return nil, nil, fmt.Errorf("invalid resource: %w", err)
		}
		delegations := cap.Nb().Delegations

		log := log.With(
			zap.Stringer("agent", agent),
			zap.Stringer("space", space),
		)
		log.Debug("delegating access", zap.Stringer("agent", agent))

		providers, err := provisioningSvc.ListServiceProviders(ctx, space)
		if err != nil {
			log.Error("failed to list service providers", zap.Error(err))
			return nil, nil, fmt.Errorf("listing service providers: %w", err)
		}
		if len(providers) == 0 {
			return result.Error[access.DelegateOk, failure.IPLDBuilderFailure](
				errors.New(InsufficientStorageErrorName, "space has no storage provider"),
			), nil, nil
		}

		inv.Proofs()

		dlgs, err := extractDelegations(cap, inv)
		if err != nil {
			log.Error("failed to extract delegations", zap.Error(err))
			return nil, nil, err
		}

		cause, err := ipldutil.ToCID(inv.Link())
		if err != nil {
			return nil, nil, err
		}
		err = delegationStore.PutMany(ctx, dlgs, cause)
		if err != nil {
			log.Error("failed to store delegations", zap.Error(err))
			return nil, nil, err
		}

		// For a mock service, we just acknowledge receipt of the delegations
		// In a real service, these would be stored for later retrieval
		for _, key := range delegations.Keys {
			link := delegations.Values[key]
			if link != nil {
				logger.Debug("stored delegation", zap.String("link", link.String()))
			}
		}

		return result.Ok[access.DelegateOk, failure.IPLDBuilderFailure](access.DelegateOk{}), nil, nil
	}
}

func extractDelegations(cap ucan.Capability[access.DelegateCaveats], inv invocation.Invocation) ([]delegation.Delegation, error) {
	br, err := blockstore.NewBlockReader(blockstore.WithBlocksIterator(inv.Blocks()))
	if err != nil {
		return nil, fmt.Errorf("creating block reader: %w", err)
	}
	dlgs := make([]delegation.Delegation, 0, len(cap.Nb().Delegations.Keys))
	for _, root := range cap.Nb().Delegations.Values {
		block, ok, err := br.Get(root)
		if err != nil {
			return nil, fmt.Errorf("getting delegation root block %q: %w", root.String(), err)
		}
		if !ok {
			return nil, errors.New(DelegationNotFoundErrorName, "delegation not found: %s", root.String())
		}
		d, err := delegation.NewDelegation(block, br)
		if err != nil {
			return nil, fmt.Errorf("creating delegation view for root %s: %w", root.String(), err)
		}
		dlgs = append(dlgs, d)
	}
	return dlgs, nil
}
