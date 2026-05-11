package memory

import (
	"bytes"
	"context"
	"slices"
	"sync"

	"github.com/fil-forge/sprue/pkg/store"
	dlgstore "github.com/fil-forge/sprue/pkg/store/delegation"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/ucan"
	cid "github.com/ipfs/go-cid"
)

type Store struct {
	mutex  sync.RWMutex
	tokens map[did.DID][]ucan.Token
}

var _ dlgstore.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		tokens: map[did.DID][]ucan.Token{},
	}
}

func (s *Store) ListByAudience(ctx context.Context, audience did.DID, options ...dlgstore.ListByAudienceOption) (store.Page[ucan.Token], error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	limit := 1000
	cfg := dlgstore.ListByAudienceConfig{Limit: &limit}
	for _, opt := range options {
		opt(&cfg)
	}
	tokens := slices.Clone(s.tokens[audience])
	if cfg.Cursor != nil {
		for i, d := range tokens {
			if d.Link().String() == *cfg.Cursor {
				if i+1 < len(tokens) {
					tokens = tokens[i+1:]
				}
				break
			}
		}
	}
	var cursor *string
	if cfg.Limit != nil && len(tokens) > *cfg.Limit {
		tokens = tokens[:*cfg.Limit]
		last := tokens[len(tokens)-1].Link().String()
		cursor = &last
	}
	return store.Page[ucan.Token]{
		Cursor:  cursor,
		Results: tokens,
	}, nil

}

func (s *Store) PutMany(ctx context.Context, tokens []ucan.Token, cause cid.Cid) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for _, d := range tokens {
		var aud did.DID
		// audience may be nil if the token is an invocation
		if d.Audience() != nil {
			aud = d.Audience().DID()
		} else {
			aud = d.Subject().DID()
		}
		s.tokens[aud] = append(s.tokens[aud], d)
		slices.SortFunc(s.tokens[aud], func(a, b ucan.Token) int {
			return bytes.Compare(a.Link().Bytes(), b.Link().Bytes())
		})
	}
	return nil
}
