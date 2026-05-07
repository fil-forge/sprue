package memory

import (
	"bytes"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/upload"
	"github.com/ipfs/go-cid"
)

type Store struct {
	mutex   sync.RWMutex
	uploads map[did.DID][]upload.UploadRecord
	shards  map[did.DID]map[cid.Cid][]cid.Cid
}

var _ upload.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		uploads: map[did.DID][]upload.UploadRecord{},
		// space -> upload root -> shards
		shards: map[did.DID]map[cid.Cid][]cid.Cid{},
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

func (m *Store) Remove(ctx context.Context, space did.DID, root cid.Cid) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	uploads, ok := m.uploads[space]
	if !ok {
		return upload.ErrUploadNotFound
	}
	idx := slices.IndexFunc(uploads, func(r upload.UploadRecord) bool {
		return r.Root.String() == root.String()
	})
	if idx == -1 {
		return upload.ErrUploadNotFound
	}
	m.uploads[space] = append(uploads[:idx], uploads[idx+1:]...)
	delete(m.shards[space], root)
	return nil
}

func (m *Store) Upsert(ctx context.Context, space did.DID, root cid.Cid, shards []cid.Cid, cause cid.Cid) error {
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
	if idx == -1 {
		uploads = append(uploads, upload.UploadRecord{
			Space:      space,
			Root:       root,
			Cause:      cause,
			InsertedAt: time.Now(),
		})
		m.uploads[space] = uploads
	} else {
		uploads[idx].UpdatedAt = time.Now()
		uploads[idx].Cause = cause
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
	return nil
}
