package memory

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/internal/timeutil"
	"github.com/fil-forge/sprue/pkg/store"
	spacediff "github.com/fil-forge/sprue/pkg/store/space_diff"
	cid "github.com/ipfs/go-cid"
)

type Store struct {
	mutex sync.RWMutex
	// provider -> space -> list of diffs (sorted by receiptAt)
	diffs map[did.DID]map[did.DID][]spacediff.DifferenceRecord
}

var _ spacediff.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		diffs: map[did.DID]map[did.DID][]spacediff.DifferenceRecord{},
	}
}

func (s *Store) List(ctx context.Context, provider did.DID, space did.DID, after time.Time, options ...spacediff.ListOption) (store.Page[spacediff.DifferenceRecord], error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	cfg := spacediff.ListConfig{}
	for _, opt := range options {
		opt(&cfg)
	}

	limit := 1000
	if cfg.Limit != nil {
		limit = *cfg.Limit
	}

	if _, ok := s.diffs[provider]; !ok {
		s.diffs[provider] = map[did.DID][]spacediff.DifferenceRecord{}
	}
	if _, ok := s.diffs[provider][space]; !ok {
		s.diffs[provider][space] = []spacediff.DifferenceRecord{}
	}

	if cfg.Cursor != nil {
		cursorTime, err := time.Parse(timeutil.SimplifiedISO8601, *cfg.Cursor)
		if err != nil {
			return store.Page[spacediff.DifferenceRecord]{}, fmt.Errorf("invalid cursor: %w", err)
		}
		after = cursorTime
	}

	diffs := []spacediff.DifferenceRecord{}
	for _, d := range s.diffs[provider][space] {
		if d.ReceiptAt.After(after) {
			diffs = append(diffs, d)
		}
	}

	var cursor *string
	if len(diffs) > limit {
		diffs = diffs[:limit]
		cursorStr := diffs[len(diffs)-1].ReceiptAt.Format(timeutil.SimplifiedISO8601)
		cursor = &cursorStr
	}

	return store.Page[spacediff.DifferenceRecord]{
		Cursor:  cursor,
		Results: diffs,
	}, nil
}

func (s *Store) Put(ctx context.Context, provider did.DID, space did.DID, subscription string, cause cid.Cid, delta int64, receiptAt time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, ok := s.diffs[provider]; !ok {
		s.diffs[provider] = map[did.DID][]spacediff.DifferenceRecord{}
	}
	if _, ok := s.diffs[provider][space]; !ok {
		s.diffs[provider][space] = []spacediff.DifferenceRecord{}
	}
	s.diffs[provider][space] = append(s.diffs[provider][space], spacediff.DifferenceRecord{
		Provider:     provider,
		Space:        space,
		Subscription: subscription,
		Cause:        cause,
		Delta:        delta,
		ReceiptAt:    receiptAt.UTC().Truncate(time.Millisecond),
		InsertedAt:   time.Now(),
	})
	slices.SortFunc(s.diffs[provider][space], func(a, b spacediff.DifferenceRecord) int {
		return a.ReceiptAt.Compare(b.ReceiptAt)
	})
	return nil
}
