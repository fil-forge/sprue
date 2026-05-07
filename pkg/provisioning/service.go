package provisioning

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/fil-forge/go-ucanto/core/ipld/codec/cbor"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	"github.com/fil-forge/sprue/pkg/store/subscription"
	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/codec/dagcbor"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/multiformats/go-multihash"
)

type (
	SpaceDID       = did.DID
	AccountDID     = did.DID
	ServiceDID     = did.DID
	SubscriptionID = string
)

const ProviderNotAllowedErrorName = "ProviderNotAllowed"

// ErrProviderNotAllowed is returned when a provider is not in the allowed list.
var ErrProviderNotAllowed = errors.New(ProviderNotAllowedErrorName, "provider not allowed")

type Service struct {
	providers         []ServiceDID
	consumerStore     consumer.Store
	subscriptionStore subscription.Store
}

func NewService(providers []ServiceDID, consumerStore consumer.Store, subscriptionStore subscription.Store) *Service {
	return &Service{
		providers:         providers,
		consumerStore:     consumerStore,
		subscriptionStore: subscriptionStore,
	}
}

// GetConsumer returns the consumer record for a given service provider and
// consumer (space). It may return [consumer.ErrConsumerNotFound].
func (s *Service) GetConsumer(ctx context.Context, provider ServiceDID, space SpaceDID) (consumer.Record, error) {
	return s.consumerStore.Get(ctx, provider, space)
}

// GetSubscription returns the subscription record for a given service
// provider. It may return [subscription.ErrSubscriptionNotFound].
func (s *Service) GetSubscription(ctx context.Context, provider ServiceDID, subscription SubscriptionID) (subscription.Record, error) {
	return s.subscriptionStore.Get(ctx, provider, subscription)
}

// ListServiceProviders returns a list of services that have been provisioned
// for the given consumer (space).
func (s *Service) ListServiceProviders(ctx context.Context, space SpaceDID) ([]ServiceDID, error) {
	var providers []ServiceDID
	page, err := s.consumerStore.List(ctx, space)
	if err != nil {
		return nil, err
	}
	for {
		for _, rec := range page.Results {
			providers = append(providers, rec.Provider)
		}
		if page.Cursor == nil {
			break
		}
		page, err = s.consumerStore.List(ctx, space, consumer.WithListCursor(*page.Cursor))
		if err != nil {
			return nil, err
		}
	}
	return providers, nil
}

// Provision provisions a service provider for a consumer (space) on behalf of
// a customer (account). It may return [customer.ErrCustomerNotFound]
// if the customer does not exist and [consumer.ErrConsumerExists] if the
// consumer is already provisioned for the provider.
func (s *Service) Provision(ctx context.Context, customer AccountDID, consumer SpaceDID, provider ServiceDID, cause cid.Cid) (SubscriptionID, error) {
	// Ensure the provider is allowed.
	if !slices.ContainsFunc(s.providers, func(p ServiceDID) bool {
		return p.String() == provider.String()
	}) {
		return "", ErrProviderNotAllowed
	}

	subscriptionID, err := NewSubscriptionID(consumer)
	if err != nil {
		return "", fmt.Errorf("creating subscription ID: %w", err)
	}

	if err := s.subscriptionStore.Add(ctx, provider, subscriptionID, customer, cause); err != nil {
		if !errors.Is(err, subscription.ErrSubscriptionExists) {
			return "", fmt.Errorf("adding subscription: %w", err)
		}
	}

	if err := s.consumerStore.Add(ctx, provider, consumer, customer, subscriptionID, cause); err != nil {
		return "", fmt.Errorf("adding consumer: %w", err)
	}

	return subscriptionID, nil
}

func NewSubscriptionID(consumer SpaceDID) (string, error) {
	nb := basicnode.Prototype.Map.NewBuilder()
	ma, err := nb.BeginMap(1)
	if err != nil {
		return "", err
	}
	na, err := ma.AssembleEntry("consumer")
	if err != nil {
		return "", err
	}
	if err := na.AssignString(consumer.String()); err != nil {
		return "", err
	}
	if err := ma.Finish(); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := dagcbor.Encode(nb.Build(), &buf); err != nil {
		return "", err
	}
	c, err := cid.Prefix{
		Version:  1,
		Codec:    cbor.Code,
		MhType:   multihash.SHA2_256,
		MhLength: -1,
	}.Sum(buf.Bytes())
	if err != nil {
		return "", err
	}
	return c.String(), nil
}
