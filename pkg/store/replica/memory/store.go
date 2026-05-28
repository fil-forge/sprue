package memory

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fil-forge/libforge/bytemap"
	"github.com/fil-forge/sprue/pkg/store/replica"
	"github.com/fil-forge/ucantone/did"
	cid "github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
)

type Store struct {
	mutex sync.RWMutex
	// space DID -> blob digest -> replica record
	replicas map[did.DID]bytemap.ByteMap[multihash.Multihash, []replica.Record]
}

var _ replica.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		replicas: make(map[did.DID]bytemap.ByteMap[multihash.Multihash, []replica.Record]),
	}
}

func (s *Store) Add(ctx context.Context, space did.DID, digest multihash.Multihash, provider did.DID, status replica.ReplicationStatus, cause cid.Cid) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, ok := s.replicas[space]; !ok {
		s.replicas[space] = bytemap.NewByteMap[multihash.Multihash, []replica.Record](-1)
	}
	replicas := s.replicas[space].Get(digest)
	for _, r := range replicas {
		if r.Space == space && bytes.Equal(r.Digest, digest) && r.Provider == provider {
			return replica.ErrReplicaExists
		}
	}
	replicas = append(replicas, replica.Record{
		Space:     space,
		Digest:    digest,
		Provider:  provider,
		Status:    status,
		Cause:     cause,
		CreatedAt: time.Now(),
	})
	// sort by provider since it is unique in this list
	slices.SortFunc(replicas, func(a, b replica.Record) int {
		return strings.Compare(a.Provider.String(), b.Provider.String())
	})
	s.replicas[space].Set(digest, replicas)
	return nil
}

func (s *Store) List(ctx context.Context, space did.DID, digest multihash.Multihash) ([]replica.Record, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	if _, ok := s.replicas[space]; !ok {
		return nil, nil
	}
	return s.replicas[space].Get(digest), nil
}

func (s *Store) Retry(ctx context.Context, space did.DID, digest multihash.Multihash, provider did.DID, status replica.ReplicationStatus, cause cid.Cid) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, ok := s.replicas[space]; !ok {
		s.replicas[space] = bytemap.NewByteMap[multihash.Multihash, []replica.Record](-1)
	}
	replicas := s.replicas[space].Get(digest)
	for i, r := range replicas {
		if r.Space == space && bytes.Equal(r.Digest, digest) && r.Provider == provider {
			replicas[i].Status = status
			replicas[i].Cause = cause
			replicas[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return replica.ErrReplicaNotFound
}

func (s *Store) SetStatus(ctx context.Context, space did.DID, digest multihash.Multihash, provider did.DID, status replica.ReplicationStatus) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, ok := s.replicas[space]; !ok {
		s.replicas[space] = bytemap.NewByteMap[multihash.Multihash, []replica.Record](-1)
	}
	replicas := s.replicas[space].Get(digest)
	for i, r := range replicas {
		if r.Space == space && bytes.Equal(r.Digest, digest) && r.Provider == provider {
			replicas[i].Status = status
			replicas[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return replica.ErrReplicaNotFound
}
