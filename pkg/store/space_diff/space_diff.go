package spacediff

import (
	"context"
	"time"

	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/ipfs/go-cid"
)

type (
	ListConfig = store.PaginationConfig
	ListOption func(cfg *ListConfig)
)

func WithListLimit(limit int) ListOption {
	return func(cfg *ListConfig) {
		cfg.Limit = &limit
	}
}

func WithListCursor(cursor string) ListOption {
	return func(cfg *ListConfig) {
		cfg.Cursor = &cursor
	}
}

type Store interface {
	Put(ctx context.Context, provider did.DID, space did.DID, subscription string, cause cid.Cid, delta int64, receiptAt time.Time) error
	// List space diffs whose receipt was issued after the given time.
	List(ctx context.Context, provider did.DID, space did.DID, after time.Time, options ...ListOption) (store.Page[DifferenceRecord], error)
}

type DifferenceRecord struct {
	// Storage provider for the space.
	Provider did.DID
	// Space DID (did:key:...).
	Space did.DID
	// Subscription in use when the size changed.
	Subscription string
	// Invocation CID that changed the space size (bafy...).
	Cause cid.Cid
	// Number of bytes added to or removed from the space.
	Delta int64
	// ISO timestamp the receipt for the change was issued.
	ReceiptAt time.Time
	// ISO timestamp we recorded the change.
	InsertedAt time.Time
}
