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
	"github.com/fil-forge/sprue/pkg/store/consumer"
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
	consumerStore  consumer.Store
	spaceMetrics   metrics.SpaceStore
	adminMetrics   metrics.Store
}

var _ blobregistry.Store = (*Store)(nil)

func New(spaceDiffStore spacediff.Store, consumerStore consumer.Store, spaceMetrics metrics.SpaceStore, adminMetrics metrics.Store) *Store {
	return &Store{
		blobs:          map[did.DID][]blobregistry.Record{},
		spaceDiffStore: spaceDiffStore,
		consumerStore:  consumerStore,
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
			consumers, err := s.collectConsumers(ctx, space)
			if err != nil {
				return fmt.Errorf("collecting consumers: %w", err)
			}
			// There should only be one subscription per provider, but in theory you
			// could have multiple providers for the same consumer (space).
			for _, c := range consumers {
				s.spaceDiffStore.Put(ctx, c.Provider, space, c.Subscription, cause, -int64(ent.Blob.Size), time.Now())
			}

			inc := map[string]uint64{
				metrics.BlobRemoveTotalMetric:     1,
				metrics.BlobRemoveSizeTotalMetric: ent.Blob.Size,
			}
			err = s.spaceMetrics.IncrementTotals(ctx, space, inc)
			if err != nil {
				return fmt.Errorf("incrementing space metrics: %w", err)
			}
			err = s.adminMetrics.IncrementTotals(ctx, inc)
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

	consumers, err := s.collectConsumers(ctx, space)
	if err != nil {
		return fmt.Errorf("collecting consumers: %w", err)
	}
	// There should only be one subscription per provider, but in theory you
	// could have multiple providers for the same consumer (space).
	for _, c := range consumers {
		s.spaceDiffStore.Put(ctx, c.Provider, space, c.Subscription, cause, int64(blob.Size), time.Now())
	}

	inc := map[string]uint64{
		metrics.BlobAddTotalMetric:     1,
		metrics.BlobAddSizeTotalMetric: blob.Size,
	}
	err = s.spaceMetrics.IncrementTotals(ctx, space, inc)
	if err != nil {
		return fmt.Errorf("incrementing space metrics: %w", err)
	}
	err = s.adminMetrics.IncrementTotals(ctx, inc)
	if err != nil {
		return fmt.Errorf("incrementing admin metrics: %w", err)
	}

	return nil
}

func (s *Store) collectConsumers(ctx context.Context, space did.DID) ([]consumer.Record, error) {
	results, err := store.Collect(ctx, func(ctx context.Context, options store.PaginationConfig) (store.Page[consumer.Record], error) {
		opts := []consumer.ListOption{}
		if options.Cursor != nil {
			opts = append(opts, consumer.WithListCursor(*options.Cursor))
		}
		return s.consumerStore.List(ctx, space, opts...)
	})
	if err != nil {
		return nil, fmt.Errorf("listing consumers: %w", err)
	}
	if len(results) == 0 {
		return nil, consumer.ErrConsumerNotFound
	}
	return results, nil
}
