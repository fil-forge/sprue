package memory

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	spacediff "github.com/fil-forge/sprue/pkg/store/space_diff"
	"github.com/fil-forge/ucantone/did"
	cid "github.com/ipfs/go-cid"
)

type Store struct {
	mutex sync.RWMutex
	// space -> list of diffs, sorted by (receiptAt, cause)
	diffs map[did.DID][]spacediff.DifferenceRecord
}

var _ spacediff.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		diffs: map[did.DID][]spacediff.DifferenceRecord{},
	}
}

func (s *Store) List(ctx context.Context, space did.DID, after time.Time, options ...spacediff.ListOption) (store.Page[spacediff.DifferenceRecord], error) {
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

	// Reading a space this store has never seen yields no diffs. Creating the
	// entry to say so would be a write, and this holds a read lock that
	// concurrent readers share.
	var (
		cursorTime  time.Time
		cursorCause string
	)
	if cfg.Cursor != nil {
		var err error
		cursorTime, cursorCause, err = store.DecodeCursor(*cfg.Cursor)
		if err != nil {
			return store.Page[spacediff.DifferenceRecord]{}, fmt.Errorf("invalid cursor: %w", err)
		}
	}

	diffs := []spacediff.DifferenceRecord{}
	for _, d := range s.diffs[space] {
		if cfg.Cursor != nil {
			// Resume within a group of diffs sharing a timestamp, rather than
			// past it, which would drop the rest of the group.
			if !afterKey(d, cursorTime, cursorCause) {
				continue
			}
		} else if !d.ReceiptAt.After(after) {
			continue
		}
		diffs = append(diffs, d)
		if len(diffs) > limit {
			break
		}
	}

	var cursor *string
	if len(diffs) > limit {
		diffs = diffs[:limit]
		last := diffs[len(diffs)-1]
		c := store.EncodeCursor(last.ReceiptAt, last.Cause.String())
		cursor = &c
	}

	return store.Page[spacediff.DifferenceRecord]{
		Cursor:  cursor,
		Results: diffs,
	}, nil
}

// afterKey reports whether a diff sorts after the (receiptAt, cause) a cursor
// names, matching the order rows are listed in.
func afterKey(d spacediff.DifferenceRecord, receiptAt time.Time, cause string) bool {
	if c := d.ReceiptAt.Compare(receiptAt); c != 0 {
		return c > 0
	}
	return d.Cause.String() > cause
}

func (s *Store) Put(ctx context.Context, space did.DID, cause cid.Cid, delta int64, receiptAt time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.diffs[space] = append(s.diffs[space], spacediff.DifferenceRecord{
		Space:      space,
		Cause:      cause,
		Delta:      delta,
		ReceiptAt:  receiptAt.UTC(),
		InsertedAt: time.Now(),
	})
	// Sorted by the same key List pages on.
	slices.SortFunc(s.diffs[space], func(a, b spacediff.DifferenceRecord) int {
		if c := a.ReceiptAt.Compare(b.ReceiptAt); c != 0 {
			return c
		}
		return strings.Compare(a.Cause.String(), b.Cause.String())
	})
	return nil
}
