// Package memory holds the in-memory blob registry, for local development and
// tests.
//
// It writes a change to the space diff log and to the space byte counters
// through two separate stores, each with its own lock, so the pair is not
// atomic: a reader can catch the diff row before the counters move. Readers
// that need the two to agree, such as the usage service, can only detect that
// by re-reading. The postgres registry commits both in one transaction and has
// no such window, which is why this stays a development backend.
package memory

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/sprue/pkg/store"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	spacediff "github.com/fil-forge/sprue/pkg/store/space_diff"
	"github.com/fil-forge/ucantone/did"
	cid "github.com/ipfs/go-cid"
	multihash "github.com/multiformats/go-multihash"
)

type Store struct {
	mutex sync.RWMutex
	// space -> list of blob entries
	blobs          map[did.DID][]blobregistry.Record
	spaceDiffStore spacediff.Store
	spaceMetrics   metrics.SpaceStore
	adminMetrics   metrics.Store
}

var _ blobregistry.Store = (*Store)(nil)

func New(spaceDiffStore spacediff.Store, spaceMetrics metrics.SpaceStore, adminMetrics metrics.Store) *Store {
	return &Store{
		blobs:          map[did.DID][]blobregistry.Record{},
		spaceDiffStore: spaceDiffStore,
		spaceMetrics:   spaceMetrics,
		adminMetrics:   adminMetrics,
	}
}

func (s *Store) Deregister(ctx context.Context, space did.DID, digest multihash.Multihash, cause cid.Cid) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	ents := []blobregistry.Record{}
	for _, ent := range s.blobs[space] {
		if bytes.Equal(ent.Blob.Digest, digest) {
			inc := map[string]uint64{
				metrics.BlobRemoveTotalMetric:     1,
				metrics.BlobRemoveSizeTotalMetric: ent.Blob.Size,
			}
			// One change is one diff row and one counter movement. The instant
			// is read once, before the write, so both describe the same event.
			receiptAt := time.Now()
			if err := s.spaceDiffStore.Put(ctx, space, cause, -int64(ent.Blob.Size), receiptAt); err != nil {
				return fmt.Errorf("putting space diff: %w", err)
			}
			if err := s.spaceMetrics.IncrementTotals(ctx, space, inc); err != nil {
				return fmt.Errorf("incrementing space metrics: %w", err)
			}

			err := s.adminMetrics.IncrementTotals(ctx, inc)
			if err != nil {
				return fmt.Errorf("incrementing admin metrics: %w", err)
			}
		} else {
			ents = append(ents, ent)
		}
	}
	if len(ents) == len(s.blobs[space]) {
		return blobregistry.ErrEntryNotFound
	}
	s.blobs[space] = ents
	return nil
}

func (s *Store) Get(ctx context.Context, space did.DID, digest multihash.Multihash) (blobregistry.Record, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	for _, ent := range s.blobs[space] {
		if bytes.Equal(ent.Blob.Digest, digest) {
			return ent, nil
		}
	}
	return blobregistry.Record{}, blobregistry.ErrEntryNotFound
}

func (s *Store) List(ctx context.Context, space did.DID, options ...blobregistry.ListOption) (store.Page[blobregistry.Record], error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	cfg := blobregistry.ListConfig{}
	for _, opt := range options {
		opt(&cfg)
	}

	entries := s.blobs[space]

	if cfg.Cursor != nil {
		found := false
		for i, ent := range entries {
			if ent.Blob.Digest.HexString() == *cfg.Cursor {
				entries = entries[i+1:]
				found = true
				break
			}
		}
		if !found {
			return store.Page[blobregistry.Record]{}, fmt.Errorf("invalid cursor")
		}
	}

	var cursor *string
	if cfg.Limit != nil && len(entries) > *cfg.Limit {
		entries = entries[:*cfg.Limit]
		c := entries[len(entries)-1].Blob.Digest.HexString()
		cursor = &c
	}

	results := make([]blobregistry.Record, len(entries))
	copy(results, entries)
	return store.Page[blobregistry.Record]{Results: results, Cursor: cursor}, nil
}

func (s *Store) Register(ctx context.Context, space did.DID, blob blob.Blob, cause cid.Cid) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for _, ent := range s.blobs[space] {
		if bytes.Equal(ent.Blob.Digest, blob.Digest) {
			return blobregistry.ErrEntryExists
		}
	}

	ent := blobregistry.Record{
		Space:      space,
		Blob:       blob,
		Cause:      cause,
		InsertedAt: time.Now(),
	}
	s.blobs[space] = append(s.blobs[space], ent)

	inc := map[string]uint64{
		metrics.BlobAddTotalMetric:     1,
		metrics.BlobAddSizeTotalMetric: blob.Size,
	}
	// One change is one diff row and one counter movement. The instant is read
	// once, before the write, so both describe the same event.
	receiptAt := time.Now()
	if err := s.spaceDiffStore.Put(ctx, space, cause, int64(blob.Size), receiptAt); err != nil {
		return fmt.Errorf("putting space diff: %w", err)
	}
	if err := s.spaceMetrics.IncrementTotals(ctx, space, inc); err != nil {
		return fmt.Errorf("incrementing space metrics: %w", err)
	}

	err := s.adminMetrics.IncrementTotals(ctx, inc)
	if err != nil {
		return fmt.Errorf("incrementing admin metrics: %w", err)
	}

	return nil
}
