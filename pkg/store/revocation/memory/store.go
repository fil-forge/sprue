package memory

import (
	"context"
	"maps"
	"sync"

	"github.com/fil-forge/sprue/pkg/store/revocation"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
)

type Store struct {
	mutex sync.RWMutex
	// delegation CID -> scope DID -> cause CID
	revocations map[cid.Cid]map[did.DID]cid.Cid
}

var _ revocation.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		revocations: map[cid.Cid]map[did.DID]cid.Cid{},
	}
}

func (s *Store) Add(ctx context.Context, delegation cid.Cid, scope did.DID, cause cid.Cid) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, ok := s.revocations[delegation]; !ok {
		s.revocations[delegation] = map[did.DID]cid.Cid{}
	}
	if _, ok := s.revocations[delegation][scope]; ok {
		return nil
	}
	s.revocations[delegation][scope] = cause
	return nil
}

func (s *Store) Find(ctx context.Context, delegations []cid.Cid) (map[cid.Cid]map[did.DID]cid.Cid, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	result := map[cid.Cid]map[did.DID]cid.Cid{}
	for _, delegation := range delegations {
		if revocations, ok := s.revocations[delegation]; ok {
			result[delegation] = maps.Clone(revocations)
		}
	}
	return result, nil
}

func (s *Store) Reset(ctx context.Context, delegation cid.Cid, scope did.DID, cause cid.Cid) error {
	s.mutex.Lock()
	s.revocations[delegation] = map[did.DID]cid.Cid{scope: cause}
	s.mutex.Unlock()
	return nil
}
