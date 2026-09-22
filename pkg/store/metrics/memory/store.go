package memory

import (
	"context"
	"sync"

	"github.com/fil-forge/sprue/pkg/store/metrics"
	"github.com/fil-forge/ucantone/did"
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

// spaceKey identifies one provider's totals for one space.
type spaceKey struct {
	provider did.DID
	space    did.DID
}

type SpaceStore struct {
	mutex   sync.RWMutex
	metrics map[spaceKey]*Store
}

var _ metrics.SpaceStore = (*SpaceStore)(nil)

func NewSpaceStore() *SpaceStore {
	return &SpaceStore{
		metrics: map[spaceKey]*Store{},
	}
}

func (a *SpaceStore) Get(ctx context.Context, provider did.DID, space did.DID) (map[string]uint64, error) {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	s, ok := a.metrics[spaceKey{provider, space}]
	if !ok {
		return map[string]uint64{}, nil
	}
	return s.Get(ctx)
}

func (a *SpaceStore) IncrementTotals(ctx context.Context, provider did.DID, space did.DID, inc map[string]uint64) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	key := spaceKey{provider, space}
	s, ok := a.metrics[key]
	if !ok {
		s = New()
		a.metrics[key] = s
	}
	return s.IncrementTotals(ctx, inc)
}
