package memory

import (
	"context"
	"sync"

	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/store/metrics"
)

type Store struct {
	mutex   sync.RWMutex
	metrics map[string]uint64
}

var _ metrics.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		metrics: map[string]uint64{},
	}
}

func (a *Store) Get(ctx context.Context) (map[string]uint64, error) {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	metrics := make(map[string]uint64, len(a.metrics))
	for k, v := range a.metrics {
		metrics[k] = v
	}
	return metrics, nil
}

func (a *Store) IncrementTotals(ctx context.Context, inc map[string]uint64) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	for k, v := range inc {
		a.metrics[k] += v
	}
	return nil
}

type SpaceStore struct {
	mutex   sync.RWMutex
	metrics map[did.DID]*Store
}

var _ metrics.SpaceStore = (*SpaceStore)(nil)

func NewSpaceStore() *SpaceStore {
	return &SpaceStore{
		metrics: map[did.DID]*Store{},
	}
}

func (a *SpaceStore) Get(ctx context.Context, space did.DID) (map[string]uint64, error) {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	s, ok := a.metrics[space]
	if !ok {
		return map[string]uint64{}, nil
	}
	return s.Get(ctx)
}

func (a *SpaceStore) IncrementTotals(ctx context.Context, space did.DID, inc map[string]uint64) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	s, ok := a.metrics[space]
	if !ok {
		s = New()
		a.metrics[space] = s
	}
	return s.IncrementTotals(ctx, inc)
}
