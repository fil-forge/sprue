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
	"github.com/fil-forge/sprue/pkg/store/consumer"
	cid "github.com/ipfs/go-cid"
)

type Store struct {
	mutex sync.RWMutex
	// space -> list of consumer records
	consumers map[did.DID][]consumer.Record
}

var _ consumer.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		consumers: map[did.DID][]consumer.Record{},
	}
}

func (s *Store) Add(ctx context.Context, provider did.DID, space did.DID, customer did.DID, subscription string, cause cid.Cid) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for _, c := range s.consumers[space] {
		if c.Provider == provider && c.Consumer == space && c.Customer == customer && c.Subscription == subscription {
			return consumer.ErrConsumerExists
		}
	}

	s.consumers[space] = append(s.consumers[space], consumer.Record{
		Provider:     provider,
		Consumer:     space,
		Customer:     customer,
		Subscription: subscription,
		Cause:        cause,
	})
	return nil
}

func (s *Store) Get(ctx context.Context, provider did.DID, space did.DID) (consumer.Record, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	for _, c := range s.consumers[space] {
		if c.Provider == provider && c.Consumer == space {
			return c, nil
		}
	}
	return consumer.Record{}, consumer.ErrConsumerNotFound
}

func (s *Store) GetBySubscription(ctx context.Context, provider did.DID, subscription string) (consumer.Record, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	for _, c := range s.consumers {
		for _, r := range c {
			if r.Provider == provider && r.Subscription == subscription {
				return r, nil
			}
		}
	}
	return consumer.Record{}, consumer.ErrConsumerNotFound
}

func (s *Store) List(ctx context.Context, space did.DID, options ...consumer.ListOption) (store.Page[consumer.Record], error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	limit := 1000
	cfg := consumer.ListConfig{Limit: &limit}
	for _, opt := range options {
		opt(&cfg)
	}

	consumers := slices.Clone(s.consumers[space])
	if cfg.Cursor != nil {
		i, err := strconv.Atoi(*cfg.Cursor)
		if err != nil {
			return store.Page[consumer.Record]{}, fmt.Errorf("invalid cursor: %w", err)
		}
		if i < 0 || i > len(consumers) {
			return store.Page[consumer.Record]{}, fmt.Errorf("cursor out of bounds")
		}
		consumers = consumers[i:]
	}

	var cursor *string
	if len(consumers) > *cfg.Limit {
		consumers = consumers[:*cfg.Limit]
		idx := slices.Index(s.consumers[space], consumers[len(consumers)-1])
		c := strconv.Itoa(idx + 1)
		cursor = &c
	}

	return store.Page[consumer.Record]{
		Cursor:  cursor,
		Results: consumers,
	}, nil
}

func (s *Store) ListByCustomer(ctx context.Context, customer did.DID, options ...consumer.ListByCustomerOption) (store.Page[consumer.Record], error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	limit := 1000
	cfg := consumer.ListConfig{Limit: &limit}
	for _, opt := range options {
		opt(&cfg)
	}

	var allConsumers []consumer.Record
	for _, c := range s.consumers {
		for _, r := range c {
			if r.Customer == customer {
				allConsumers = append(allConsumers, r)
			}
		}
	}
	slices.SortFunc(allConsumers, func(a, b consumer.Record) int {
		return strings.Compare(a.Consumer.String(), b.Consumer.String())
	})

	consumers := slices.Clone(allConsumers)
	if cfg.Cursor != nil {
		i, err := strconv.Atoi(*cfg.Cursor)
		if err != nil {
			return store.Page[consumer.Record]{}, fmt.Errorf("invalid cursor: %w", err)
		}
		if i < 0 || i > len(consumers) {
			return store.Page[consumer.Record]{}, fmt.Errorf("cursor out of bounds")
		}
		consumers = consumers[i:]
	}

	var cursor *string
	if len(consumers) > *cfg.Limit {
		consumers = consumers[:*cfg.Limit]
		idx := slices.Index(allConsumers, consumers[len(consumers)-1])
		c := strconv.Itoa(idx + 1)
		cursor = &c
	}

	return store.Page[consumer.Record]{
		Cursor:  cursor,
		Results: consumers,
	}, nil
}
