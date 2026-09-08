package memory

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	routingpolicy "github.com/fil-forge/sprue/pkg/store/routing_policy"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
)

type Store struct {
	mutex    sync.RWMutex
	policies map[did.DID]routingpolicy.PolicyRecord
	spaces   map[did.DID]routingpolicy.SpaceRecord
}

var _ routingpolicy.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		policies: map[did.DID]routingpolicy.PolicyRecord{},
		spaces:   map[did.DID]routingpolicy.SpaceRecord{},
	}
}

func (s *Store) SetCandidates(ctx context.Context, policy did.DID, candidates []did.DID, cause cid.Cid) error {
	if len(candidates) == 0 {
		return fmt.Errorf("missing candidates")
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	now := time.Now()
	if rec, ok := s.policies[policy]; ok {
		rec.Candidates = slices.Clone(candidates)
		rec.Cause = cause
		rec.UpdatedAt = now
		s.policies[policy] = rec
		return nil
	}
	s.policies[policy] = routingpolicy.PolicyRecord{
		Policy:     policy,
		Candidates: slices.Clone(candidates),
		Cause:      cause,
		InsertedAt: now,
		UpdatedAt:  now,
	}
	return nil
}

func (s *Store) GetPolicy(ctx context.Context, policy did.DID) (routingpolicy.PolicyRecord, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	rec, ok := s.policies[policy]
	if !ok {
		return routingpolicy.PolicyRecord{}, routingpolicy.ErrPolicyNotFound
	}
	rec.Candidates = slices.Clone(rec.Candidates)
	return rec, nil
}

func (s *Store) SetSpacePolicy(ctx context.Context, space did.DID, policy did.DID, cause cid.Cid) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, ok := s.policies[policy]; !ok {
		return routingpolicy.ErrPolicyNotFound
	}
	now := time.Now()
	if rec, ok := s.spaces[space]; ok {
		rec.Policy = policy
		rec.Cause = cause
		rec.UpdatedAt = now
		s.spaces[space] = rec
		return nil
	}
	s.spaces[space] = routingpolicy.SpaceRecord{
		Space:      space,
		Policy:     policy,
		Cause:      cause,
		InsertedAt: now,
		UpdatedAt:  now,
	}
	return nil
}

func (s *Store) ClearSpacePolicy(ctx context.Context, space did.DID) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.spaces, space)
	return nil
}

func (s *Store) GetSpacePolicy(ctx context.Context, space did.DID) (routingpolicy.SpaceRecord, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	rec, ok := s.spaces[space]
	if !ok {
		return routingpolicy.SpaceRecord{}, routingpolicy.ErrSpacePolicyNotFound
	}
	return rec, nil
}
