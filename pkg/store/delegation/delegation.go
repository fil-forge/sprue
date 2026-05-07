package delegation

import (
	"context"

	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/ipfs/go-cid"
)

type (
	ListByAudienceConfig = store.PaginationConfig
	ListByAudienceOption func(cfg *ListByAudienceConfig)
)

func WithListByAudienceLimit(limit int) ListByAudienceOption {
	return func(cfg *ListByAudienceConfig) { cfg.Limit = &limit }
}

func WithListByAudienceCursor(cursor string) ListByAudienceOption {
	return func(cfg *ListByAudienceConfig) { cfg.Cursor = &cursor }
}

type Store interface {
	// Write several items into storage.
	//
	// Implementations MAY choose to avoid storing delegations as long as they can
	// reliably retrieve the invocation by CID when they need to return the given
	// delegations.
	PutMany(ctx context.Context, delegations []delegation.Delegation, cause cid.Cid) error
	ListByAudience(ctx context.Context, audience did.DID, options ...ListByAudienceOption) (store.Page[delegation.Delegation], error)
}
