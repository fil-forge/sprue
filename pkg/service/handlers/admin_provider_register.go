package handlers

import (
	"context"
	"fmt"
	"net/url"

	"go.uber.org/zap"

	"github.com/fil-forge/go-ucanto/core/dag/blockstore"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/pkg/capabilities/admin/provider"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
)

var (
	initialWeight            = 0
	initialReplicationWeight = 0
)

// WithAdminProviderRegisterMethod registers the admin/provider/register handler.
func WithAdminProviderRegisterMethod(id *identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		provider.RegisterAbility,
		server.Provide(
			provider.Register,
			AdminProviderRegisterHandler(id, providerStore, logger),
		),
	)
}

func AdminProviderRegisterHandler(id *identity.Identity, providerStore storageprovider.Store, logger *zap.Logger) server.HandlerFunc[provider.RegisterCaveats, provider.RegisterOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", provider.RegisterAbility))
	return func(ctx context.Context,
		cap ucan.Capability[provider.RegisterCaveats],
		inv invocation.Invocation,
		iCtx server.InvocationContext,
	) (result.Result[provider.RegisterOk, failure.IPLDBuilderFailure], fx.Effects, error) {
		args := cap.Nb()

		endpoint, err := url.Parse(args.Endpoint)
		if err != nil {
			log.Warn("Invalid endpoint", zap.String("endpoint", args.Endpoint), zap.Error(err))
			return result.Error[provider.RegisterOk, failure.IPLDBuilderFailure](
				errors.New("InvalidEndpoint", "parsing endpoint: %s", err.Error()),
			), nil, nil
		}
		bs, err := blockstore.NewBlockReader(blockstore.WithBlocksIterator(inv.Blocks()))
		if err != nil {
			log.Error("Failed to create block reader", zap.Error(err))
			return nil, nil, fmt.Errorf("creating block reader: %w", err)
		}
		proof, err := delegation.NewDelegationView(args.Proof, bs)
		if err != nil {
			log.Error("Failed to create proof delegation view", zap.Error(err))
			return nil, nil, fmt.Errorf("creating proof delegation view: %w", err)
		}

		if inv.Issuer().DID() != id.Signer.DID() && inv.Issuer().DID() != proof.Issuer().DID() {
			log.Warn("Unauthorized access attempt", zap.Stringer("issuer", inv.Issuer().DID()))
			return result.Error[provider.RegisterOk, failure.IPLDBuilderFailure](
				errors.New("Unauthorized", "only the service identity or the provider itself can register a provider"),
			), nil, nil
		}

		_, err = providerStore.Get(ctx, proof.Issuer().DID())
		if err != nil {
			if !errors.Is(err, storageprovider.ErrStorageProviderNotFound) {
				log.Error("Failed to get existing provider", zap.Error(err))
				return nil, nil, err
			}
		} else {
			log.Warn("Provider already registered", zap.Stringer("provider", proof.Issuer().DID()))
			return result.Error[provider.RegisterOk, failure.IPLDBuilderFailure](
				errors.New("ProviderAlreadyRegistered", "a provider with this DID is already registered"),
			), nil, nil
		}

		err = providerStore.Put(ctx, *endpoint, proof, initialWeight, &initialReplicationWeight)
		if err != nil {
			log.Error("Failed to register provider", zap.Error(err))
			return nil, nil, err
		}
		return result.Ok[provider.RegisterOk, failure.IPLDBuilderFailure](provider.RegisterOk{}), nil, nil
	}
}
