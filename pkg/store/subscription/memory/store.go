package memory

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/subscription"
	cid "github.com/ipfs/go-cid"
)

type Store struct {
	mutex sync.RWMutex
	// provider DID -> subscription ID -> subscription record
	subs map[did.DID]map[string]subscription.Record
}

var _ subscription.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		subs: map[did.DID]map[string]subscription.Record{},
	}
}

func (s *Store) Add(ctx context.Context, provider did.DID, subscriptionID string, customer did.DID, cause cid.Cid) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, ok := s.subs[provider]; !ok {
		s.subs[provider] = map[string]subscription.Record{}
	}
	if _, ok := s.subs[provider][subscriptionID]; ok {
		return subscription.ErrSubscriptionExists
	}
	s.subs[provider][subscriptionID] = subscription.Record{
		Provider:     provider,
		Subscription: subscriptionID,
		Customer:     customer,
		Cause:        cause,
	}
	return nil
}

func (s *Store) Get(ctx context.Context, provider did.DID, subscriptionID string) (subscription.Record, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	if subs, ok := s.subs[provider]; ok {
		if sub, ok := subs[subscriptionID]; ok {
			return sub, nil
		}
	}
	return subscription.Record{}, subscription.ErrSubscriptionNotFound
}

func (s *Store) ListByProviderAndCustomer(ctx context.Context, provider did.DID, customer did.DID, options ...subscription.ListByProviderAndCustomerOption) (store.Page[subscription.Record], error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	limit := 1000
	cfg := subscription.ListByProviderAndCustomerConfig{Limit: &limit}
	for _, opt := range options {
		opt(&cfg)
	}

	var all []subscription.Record
	for _, sub := range s.subs[provider] {
		if sub.Customer == customer {
			all = append(all, sub)
		}
	}
	slices.SortFunc(all, func(a, b subscription.Record) int {
		return strings.Compare(a.Subscription, b.Subscription)
	})

	results := slices.Clone(all)
	if cfg.Cursor != nil {
		i, err := strconv.Atoi(*cfg.Cursor)
		if err != nil {
			return store.Page[subscription.Record]{}, fmt.Errorf("invalid cursor: %w", err)
		}
		if i < 0 || i > len(results) {
			return store.Page[subscription.Record]{}, fmt.Errorf("cursor out of bounds")
		}
		results = results[i:]
	}

	var cursor *string
	if len(results) > *cfg.Limit {
		results = results[:*cfg.Limit]
		idx := slices.Index(all, results[len(results)-1])
		c := strconv.Itoa(idx + 1)
		cursor = &c
	}

	return store.Page[subscription.Record]{
		Cursor:  cursor,
		Results: results,
	}, nil
}
