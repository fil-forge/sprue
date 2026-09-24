package memory

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	uploaddiff "github.com/fil-forge/sprue/pkg/store/upload_diff"
	"github.com/fil-forge/ucantone/did"
	cid "github.com/ipfs/go-cid"
)

type Store struct {
	mutex sync.RWMutex
	// space -> list of diffs, ordered the way Postgres lists them
	diffs map[did.DID][]uploaddiff.DifferenceRecord
}

var _ uploaddiff.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		diffs: map[did.DID][]uploaddiff.DifferenceRecord{},
	}
}

// defaultListLimit matches the Postgres implementation's page size.
const defaultListLimit = 1000

func (s *Store) List(ctx context.Context, space did.DID, after time.Time, options ...uploaddiff.ListOption) (store.Page[uploaddiff.DifferenceRecord], error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	cfg := uploaddiff.ListConfig{}
	for _, opt := range options {
		opt(&cfg)
	}
	// A non-positive limit means "unset", as it does in Postgres. Honouring a
	// zero would slice the page to [:0] and then index its last element.
	limit := defaultListLimit
	if cfg.Limit != nil && *cfg.Limit > 0 {
		limit = *cfg.Limit
	}

	// Read-only: a missing space reads as an absent entry and ranges as empty.
	// Creating it here would write under the read lock, and two concurrent
	// readers would race on the same map.
	rows := s.diffs[space]

	var (
		cursorAt    time.Time
		cursorCause string
		haveCursor  bool
	)
	if cfg.Cursor != nil {
		var err error
		cursorAt, cursorCause, err = decodeCursor(*cfg.Cursor)
		if err != nil {
			return store.Page[uploaddiff.DifferenceRecord]{}, fmt.Errorf("invalid cursor: %w", err)
		}
		haveCursor = true
	}

	diffs := []uploaddiff.DifferenceRecord{}
	for _, d := range rows {
		// The cursor compares on (receipt_at, cause), the order rows are
		// listed in. Comparing the timestamp alone would skip every row that
		// shares the last row's timestamp — and one recorded change writes a
		// row per consumer, all at the same instant.
		if haveCursor {
			if d.ReceiptAt.Before(cursorAt) {
				continue
			}
			if d.ReceiptAt.Equal(cursorAt) && d.Cause.String() <= cursorCause {
				continue
			}
		} else if !d.ReceiptAt.After(after) {
			continue
		}
		diffs = append(diffs, d)
	}

	var cursor *string
	if len(diffs) > limit {
		diffs = diffs[:limit]
		last := diffs[len(diffs)-1]
		c := encodeCursor(last.ReceiptAt, last.Cause.String())
		cursor = &c
	}

	return store.Page[uploaddiff.DifferenceRecord]{
		Cursor:  cursor,
		Results: diffs,
	}, nil
}

func (s *Store) Put(ctx context.Context, space did.DID, cause cid.Cid, delta int64, receiptAt time.Time) (bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	at := uploaddiff.Instant(receiptAt)
	// The same (space, receipt_at, cause) is one change recorded twice, which
	// Postgres suppresses with ON CONFLICT DO NOTHING. Both backends have to
	// answer the same way, since the caller gates its counters on it.
	for _, d := range s.diffs[space] {
		if d.Cause.Equals(cause) && d.ReceiptAt.Equal(at) {
			return false, nil
		}
	}

	s.diffs[space] = append(s.diffs[space], uploaddiff.DifferenceRecord{
		Space:      space,
		Cause:      cause,
		Delta:      delta,
		ReceiptAt:  at,
		InsertedAt: time.Now(),
	})
	// Sorted the way Postgres lists them, receipt_at then cause, so a cursor
	// means the same thing in both backends.
	slices.SortFunc(s.diffs[space], func(a, b uploaddiff.DifferenceRecord) int {
		if c := a.ReceiptAt.Compare(b.ReceiptAt); c != 0 {
			return c
		}
		return strings.Compare(a.Cause.String(), b.Cause.String())
	})
	return true, nil
}

// The cursor carries the last row's (receipt_at, cause), the pair the listing
// is ordered by.
func encodeCursor(receiptAt time.Time, cause string) string {
	return receiptAt.UTC().Format(time.RFC3339Nano) + "\x00" + cause
}

func decodeCursor(cursor string) (time.Time, string, error) {
	at, cause, ok := strings.Cut(cursor, "\x00")
	if !ok {
		return time.Time{}, "", fmt.Errorf("malformed cursor")
	}
	receiptAt, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, "", err
	}
	return receiptAt, cause, nil
}
