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
	"github.com/fil-forge/ucantone/did"
	cid "github.com/ipfs/go-cid"
	multihash "github.com/multiformats/go-multihash"
)

type Store struct {
	mutex sync.RWMutex
	// space -> list of allocation entries
	allocs map[did.DID][]allocation.Record
}

var _ allocation.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		allocs: map[did.DID][]allocation.Record{},
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

	s.allocs[space] = append(s.allocs[space], allocation.Record{
		Space:      space,
		Blob:       blob,
		Cause:      cause,
		InsertedAt: time.Now(),
	})
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

	ents := []allocation.Record{}
	for _, ent := range s.allocs[space] {
		if !bytes.Equal(ent.Blob.Digest, digest) {
			ents = append(ents, ent)
		}
	}
	if len(ents) == len(s.allocs[space]) {
		return allocation.ErrEntryNotFound
	}
	s.allocs[space] = ents
	return nil
}
