package memory

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/customer"
	"github.com/fil-forge/ucantone/did"
)

type Store struct {
	mutex     sync.RWMutex
	customers []customer.Record
}

var _ customer.Store = (*Store)(nil)

func New() *Store {
	return &Store{}
}

func (s *Store) Get(ctx context.Context, customerID did.DID) (customer.Record, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	for _, c := range s.customers {
		if c.Customer == customerID {
			return c, nil
		}
	}
	return customer.Record{}, customer.ErrCustomerNotFound
}

func (s *Store) List(ctx context.Context, options ...customer.ListOption) (store.Page[customer.Record], error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	limit := 1000
	cfg := customer.ListConfig{Limit: &limit}
	for _, opt := range options {
		opt(&cfg)
	}
	entries := slices.Clone(s.customers)
	if cfg.Cursor != nil {
		for i, c := range entries {
			if c.Customer.String() == *cfg.Cursor {
				if i+1 < len(entries) {
					entries = entries[i+1:]
				}
				break
			}
		}
	}
	var cursor *string
	if cfg.Limit != nil && len(entries) > *cfg.Limit {
		entries = entries[:*cfg.Limit]
		last := entries[*cfg.Limit-1].Customer.String()
		cursor = &last
	}
	return store.Page[customer.Record]{
		Results: entries,
		Cursor:  cursor,
	}, nil
}

func (s *Store) Add(ctx context.Context, customerID did.DID, externalAccount *string, product did.DID, details map[string]any, reservedCapacity *uint64) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for _, c := range s.customers {
		if c.Customer == customerID {
			return customer.ErrCustomerExists
		}
	}
	c := customer.Record{
		Customer:         customerID,
		ExternalAccount:  externalAccount,
		Product:          product,
		Details:          details,
		ReservedCapacity: reservedCapacity,
		InsertedAt:       time.Now(),
	}
	s.customers = append(s.customers, c)
	slices.SortFunc(s.customers, func(a, b customer.Record) int {
		return strings.Compare(a.Customer.String(), b.Customer.String())
	})
	return nil
}

func (s *Store) UpdateProduct(ctx context.Context, customerID did.DID, product did.DID) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for i, c := range s.customers {
		if c.Customer == customerID {
			c.Product = product
			c.UpdatedAt = time.Now()
			s.customers[i] = c
			return nil
		}
	}
	return customer.ErrCustomerNotFound
}
