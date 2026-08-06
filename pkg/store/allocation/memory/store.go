package memory

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/libforge/digestutil"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/allocation"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	spacediff "github.com/fil-forge/sprue/pkg/store/space_diff"
	"github.com/fil-forge/ucantone/did"
	cid "github.com/ipfs/go-cid"
	multihash "github.com/multiformats/go-multihash"
)

type Store struct {
	mutex sync.RWMutex
	// space -> list of allocation entries
	allocs         map[did.DID][]allocation.Record
	spaceDiffStore spacediff.Store
	consumerStore  consumer.Store
	spaceMetrics   metrics.SpaceStore
	adminMetrics   metrics.Store
}

var _ allocation.Store = (*Store)(nil)

func New(spaceDiffStore spacediff.Store, consumerStore consumer.Store, spaceMetrics metrics.SpaceStore, adminMetrics metrics.Store) *Store {
	return &Store{
		allocs:         map[did.DID][]allocation.Record{},
		spaceDiffStore: spaceDiffStore,
		consumerStore:  consumerStore,
		spaceMetrics:   spaceMetrics,
		adminMetrics:   adminMetrics,
	}
}

func (s *Store) Add(ctx context.Context, space did.DID, blob blob.Blob, cause cid.Cid) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for _, ent := range s.allocs[space] {
		if bytes.Equal(ent.Blob.Digest, blob.Digest) {
			return allocation.ErrEntryExists
		}
	}

	// Collect consumers before mutating state so a failure leaves no
	// allocation record without its space diff.
	consumers, err := consumer.CollectForSpace(ctx, s.consumerStore, space)
	if err != nil {
		return fmt.Errorf("collecting consumers: %w", err)
	}

	rec := allocation.Record{
		Space:      space,
		Blob:       blob,
		Cause:      cause,
		InsertedAt: time.Now(),
	}

	// There should only be one subscription per provider, but in theory you
	// could have multiple providers for the same consumer (space).
	for _, c := range consumers {
		if err := s.spaceDiffStore.Put(ctx, c.Provider, space, c.Subscription, cause, int64(blob.Size), time.Now()); err != nil {
			return fmt.Errorf("putting space diff: %w", err)
		}
	}

	inc := map[string]uint64{
		metrics.BlobAddTotalMetric:     1,
		metrics.BlobAddSizeTotalMetric: blob.Size,
	}
	if err := s.spaceMetrics.IncrementTotals(ctx, space, inc); err != nil {
		return fmt.Errorf("incrementing space metrics: %w", err)
	}
	if err := s.adminMetrics.IncrementTotals(ctx, inc); err != nil {
		return fmt.Errorf("incrementing admin metrics: %w", err)
	}

	s.allocs[space] = append(s.allocs[space], rec)

	return nil
}

func (s *Store) Get(ctx context.Context, space did.DID, digest multihash.Multihash) (allocation.Record, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	for _, ent := range s.allocs[space] {
		if bytes.Equal(ent.Blob.Digest, digest) {
			return ent, nil
		}
	}
	return allocation.Record{}, allocation.ErrEntryNotFound
}

func (s *Store) List(ctx context.Context, space did.DID, options ...allocation.ListOption) (store.Page[allocation.Record], error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	cfg := allocation.ListConfig{}
	for _, opt := range options {
		opt(&cfg)
	}

	entries := s.allocs[space]

	if cfg.Cursor != nil {
		found := false
		for i, ent := range entries {
			if digestutil.Format(ent.Blob.Digest) == *cfg.Cursor {
				entries = entries[i+1:]
				found = true
				break
			}
		}
		if !found {
			return store.Page[allocation.Record]{}, fmt.Errorf("invalid cursor")
		}
	}

	var cursor *string
	if cfg.Limit != nil && len(entries) > *cfg.Limit {
		entries = entries[:*cfg.Limit]
		c := digestutil.Format(entries[len(entries)-1].Blob.Digest)
		cursor = &c
	}

	results := make([]allocation.Record, len(entries))
	copy(results, entries)
	return store.Page[allocation.Record]{Results: results, Cursor: cursor}, nil
}

func (s *Store) Remove(ctx context.Context, space did.DID, digest multihash.Multihash, cause cid.Cid) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Find the record first: the not-found error takes precedence over a
	// consumer lookup failure, and its size feeds the negative space diff.
	idx := -1
	for i, ent := range s.allocs[space] {
		if bytes.Equal(ent.Blob.Digest, digest) {
			idx = i
			break
		}
	}
	if idx == -1 {
		return allocation.ErrEntryNotFound
	}
	size := s.allocs[space][idx].Blob.Size

	// Collect consumers before mutating state so a failure leaves the
	// allocation record with its space diff intact.
	consumers, err := consumer.CollectForSpace(ctx, s.consumerStore, space)
	if err != nil {
		return fmt.Errorf("collecting consumers: %w", err)
	}

	s.allocs[space] = append(s.allocs[space][:idx], s.allocs[space][idx+1:]...)

	// There should only be one subscription per provider, but in theory you
	// could have multiple providers for the same consumer (space).
	for _, c := range consumers {
		s.spaceDiffStore.Put(ctx, c.Provider, space, c.Subscription, cause, -int64(size), time.Now())
	}

	inc := map[string]uint64{
		metrics.BlobRemoveTotalMetric:     1,
		metrics.BlobRemoveSizeTotalMetric: size,
	}
	if err := s.spaceMetrics.IncrementTotals(ctx, space, inc); err != nil {
		return fmt.Errorf("incrementing space metrics: %w", err)
	}
	if err := s.adminMetrics.IncrementTotals(ctx, inc); err != nil {
		return fmt.Errorf("incrementing admin metrics: %w", err)
	}

	return nil
}
