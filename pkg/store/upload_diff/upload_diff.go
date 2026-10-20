// Package uploaddiff records per-space object-count changes as an append-only
// signed-delta log, so a caller can reconstruct the object count at any past
// time and bucket it into windows.
//
// It is the object-count counterpart to space_diff, which does the same for
// bytes. The two are separate tables because their deltas are in different
// units: summing space_diff gives bytes stored, summing upload_diff gives
// objects stored, and mixing them would corrupt both.
//
// It also carries no provider or subscription, where space_diff does. Bytes are
// billed by the provider storing them, so that log is per provider; an object
// count is a property of the space, and counting one object once per provider
// would simply multiply it.
package uploaddiff

import (
	"context"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
)

// Instant normalizes a receipt time to the precision the log keeps it at.
//
// Both backends run it before storing or comparing, so they agree on when a
// change happened and on whether two are the same one. Postgres holds a
// TIMESTAMPTZ to the microsecond, so that is the precision: normalizing before
// the write also leaves the database nothing to round, which would otherwise
// put a different value on disk from the one the caller compared against.
func Instant(t time.Time) time.Time {
	return t.UTC().Truncate(time.Microsecond)
}

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
	// Put records one object-count change, and reports whether it was
	// recorded: false means the log already held an identical one and this
	// call changed nothing. A caller that also keeps a running total has to
	// honour that, or a replay moves the total while the log stays put and the
	// two stop agreeing.
	Put(ctx context.Context, space did.DID, cause cid.Cid, delta int64, receiptAt time.Time) (bool, error)
	// List upload diffs whose receipt was issued after the given time.
	List(ctx context.Context, space did.DID, after time.Time, options ...ListOption) (store.Page[DifferenceRecord], error)
}

type DifferenceRecord struct {
	// Space DID (did:key:...).
	Space did.DID
	// `/upload/add` or `/upload/remove` task CID that changed the object count.
	Cause cid.Cid
	// Objects added to (+1) or removed from (-1) the space.
	Delta int64
	// ISO timestamp the receipt for the change was issued.
	ReceiptAt time.Time
	// ISO timestamp we recorded the change.
	InsertedAt time.Time
}
