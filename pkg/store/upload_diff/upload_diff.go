// Package uploaddiff records per-space object-count changes as an append-only
// signed-delta log, so a caller can reconstruct the object count at any past
// time and bucket it into windows.
//
// It is the object-count counterpart to space_diff, which does the same for
// bytes. The two are separate tables because their deltas are in different
// units: summing space_diff gives bytes stored, summing upload_diff gives
// objects stored, and mixing them would corrupt both.
package uploaddiff

import (
	"context"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/ucantone/did"
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
	// List upload diffs whose receipt was issued after the given time.
	List(ctx context.Context, provider did.DID, space did.DID, after time.Time, options ...ListOption) (store.Page[DifferenceRecord], error)
}

type DifferenceRecord struct {
	// Storage provider for the space.
	Provider did.DID
	// Space DID (did:key:...).
	Space did.DID
	// Subscription in use when the object count changed.
	Subscription string
	// Invocation CID that changed the object count (bafy...).
	Cause cid.Cid
	// Objects added to (+1) or removed from (-1) the space.
	Delta int64
	// ISO timestamp the receipt for the change was issued.
	ReceiptAt time.Time
	// ISO timestamp we recorded the change.
	InsertedAt time.Time
}
