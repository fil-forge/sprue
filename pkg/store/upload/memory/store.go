package memory

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	"github.com/fil-forge/sprue/pkg/store/upload"
	uploaddiff "github.com/fil-forge/sprue/pkg/store/upload_diff"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
)

type Store struct {
	mutex           sync.RWMutex
	uploads         map[did.DID][]upload.UploadRecord
	shards          map[did.DID]map[cid.Cid][]cid.Cid
	uploadDiffStore uploaddiff.Store
	spaceMetrics    metrics.SpaceStore
	adminMetrics    metrics.Store
}

var _ upload.Store = (*Store)(nil)

func New(uploadDiffStore uploaddiff.Store, spaceMetrics metrics.SpaceStore, adminMetrics metrics.Store) *Store {
	return &Store{
		uploads: map[did.DID][]upload.UploadRecord{},
		// space -> upload root -> shards
		shards:          map[did.DID]map[cid.Cid][]cid.Cid{},
		uploadDiffStore: uploadDiffStore,
		spaceMetrics:    spaceMetrics,
		adminMetrics:    adminMetrics,
	}
}

func (m *Store) Exists(ctx context.Context, space did.DID, root cid.Cid) (bool, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	uploads, ok := m.uploads[space]
	return ok && slices.ContainsFunc(uploads, func(r upload.UploadRecord) bool {
		return r.Root.String() == root.String()
	}), nil
}

func (m *Store) Get(ctx context.Context, space did.DID, root cid.Cid) (upload.UploadRecord, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	uploads := m.uploads[space]
	for _, r := range uploads {
		if r.Root.String() == root.String() {
			return r, nil
		}
	}
	return upload.UploadRecord{}, upload.ErrUploadNotFound
}

func (m *Store) Inspect(ctx context.Context, root cid.Cid) (upload.UploadInspectRecord, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	spaces := []did.DID{}
	for space, uploads := range m.uploads {
		if slices.ContainsFunc(uploads, func(r upload.UploadRecord) bool {
			return r.Root.String() == root.String()
		}) {
			spaces = append(spaces, space)
		}
	}
	return upload.UploadInspectRecord{Spaces: spaces}, nil
}

func (m *Store) List(ctx context.Context, space did.DID, options ...upload.ListOption) (store.Page[upload.UploadRecord], error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	limit := 1000
	cfg := upload.ListConfig{Limit: &limit}
	for _, option := range options {
		option(&cfg)
	}
	uploads := slices.Clone(m.uploads[space])
	if cfg.Cursor != nil {
		idx := slices.IndexFunc(uploads, func(r upload.UploadRecord) bool {
			return r.Root.String() == *cfg.Cursor
		})
		if idx != -1 {
			uploads = uploads[idx+1:]
		}
	}
	if len(uploads) > *cfg.Limit {
		uploads = uploads[:*cfg.Limit]
	}
	var cursor *string
	if len(uploads) > 0 {
		c := uploads[len(uploads)-1].Root.String()
		cursor = &c
	}
	return store.Page[upload.UploadRecord]{Results: uploads, Cursor: cursor}, nil
}

func (m *Store) ListShards(ctx context.Context, space did.DID, root cid.Cid, options ...upload.ListShardsOption) (store.Page[cid.Cid], error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	cfg := upload.ListShardsConfig{}
	for _, option := range options {
		option(&cfg)
	}
	shardsByUpload, ok := m.shards[space]
	if !ok {
		return store.Page[cid.Cid]{}, nil
	}
	shards := shardsByUpload[root]
	if cfg.Cursor != nil {
		idx := slices.IndexFunc(shards, func(l cid.Cid) bool {
			return l.String() == *cfg.Cursor
		})
		if idx != -1 {
			shards = shards[idx+1:]
		}
	}
	if cfg.Limit != nil && len(shards) > *cfg.Limit {
		shards = shards[:*cfg.Limit]
	}
	var cursor *string
	if len(shards) > 0 {
		c := shards[len(shards)-1].String()
		cursor = &c
	}
	return store.Page[cid.Cid]{Results: shards, Cursor: cursor}, nil
}

func (m *Store) Remove(ctx context.Context, space did.DID, root cid.Cid, cause cid.Cid) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	uploads, ok := m.uploads[space]
	if !ok {
		return upload.ErrUploadNotFound
	}
	idx := slices.IndexFunc(uploads, func(r upload.UploadRecord) bool {
		return r.Root.String() == root.String()
	})
	// Nothing was removed, so nothing is counted, and the missing root is
	// reported ahead of anything else that may be wrong with the space: the
	// handler turns this one error into idempotent success.
	if idx == -1 {
		return upload.ErrUploadNotFound
	}
	m.uploads[space] = append(uploads[:idx], uploads[idx+1:]...)
	delete(m.shards[space], root)
	return m.recordDelta(ctx, space, cause, -1, metrics.UploadRemoveTotalMetric)
}

func (m *Store) Upsert(ctx context.Context, space did.DID, root cid.Cid, index *cid.Cid, shards []cid.Cid, cause cid.Cid) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	uploads, ok := m.uploads[space]
	if !ok {
		uploads = []upload.UploadRecord{}
		m.uploads[space] = uploads
	}
	idx := slices.IndexFunc(uploads, func(r upload.UploadRecord) bool {
		return r.Root.String() == root.String()
	})
	inserted := idx == -1
	if inserted {
		uploads = append(uploads, upload.UploadRecord{
			Space:      space,
			Root:       root,
			Index:      index,
			Cause:      cause,
			InsertedAt: time.Now(),
		})
		m.uploads[space] = uploads
	} else {
		uploads[idx].UpdatedAt = time.Now()
		uploads[idx].Cause = cause
		uploads[idx].Index = index
	}
	shardsByUpload, ok := m.shards[space]
	if !ok {
		shardsByUpload = map[cid.Cid][]cid.Cid{}
		shardsByUpload[root] = shards
		m.shards[space] = shardsByUpload
	} else {
		for _, s := range shards {
			if !slices.ContainsFunc(shardsByUpload[root], func(l cid.Cid) bool {
				return l.String() == s.String()
			}) {
				shardsByUpload[root] = append(shardsByUpload[root], s)
			}
		}
	}
	slices.SortFunc(shardsByUpload[root], func(a, b cid.Cid) int {
		return bytes.Compare(a.Bytes(), b.Bytes())
	})
	// Only a new root counts. The capability is an upsert by spec — adding the
	// same root again merges shards and replaces the index — so a client retry
	// must leave the count where it was.
	if !inserted {
		return nil
	}
	return m.recordDelta(ctx, space, cause, 1, metrics.UploadAddTotalMetric)
}

// recordDelta writes one object-count change: one upload_diff row and the
// matching metric increment at both the space and admin scope. metric is the
// counter to bump (adds and removes have their own, each monotonically
// increasing); delta is the signed change the diff log carries, so the count at
// a time is the cumulative sum up to it.
//
// Callers have already changed the upload maps by the time this runs, and this
// store has no transaction to undo that with. It is safe because none of the
// stores it writes to can fail: they only ever append to a map under their own
// lock. A fallible store wired in here would tear the two apart, so it would
// have to come with rollback.
func (m *Store) recordDelta(ctx context.Context, space did.DID, cause cid.Cid, delta int64, metric string) error {
	recorded, err := m.uploadDiffStore.Put(ctx, space, cause, delta, time.Now())
	if err != nil {
		return fmt.Errorf("putting upload diff: %w", err)
	}
	// The log already held this change, so the counter must not move for it
	// either: the counter is what the log is anchored on, and one advancing
	// without the other leaves a reconstructed count permanently offset.
	if !recorded {
		return nil
	}

	inc := map[string]uint64{metric: 1}
	if err := m.spaceMetrics.IncrementTotals(ctx, space, inc); err != nil {
		return fmt.Errorf("incrementing space metrics: %w", err)
	}
	if err := m.adminMetrics.IncrementTotals(ctx, inc); err != nil {
		return fmt.Errorf("incrementing admin metrics: %w", err)
	}
	return nil
}
