package spacediff

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
)

// Diffs are listed in (receiptAt, cause) order, and a cursor carries both so a
// page boundary falling inside a group of diffs sharing a timestamp resumes
// within that group rather than skipping the rest of it. Every backend uses
// this encoding, so a cursor means the same thing whichever one issued it.
type cursorPayload struct {
	ReceiptAt time.Time `json:"r"`
	Cause     string    `json:"c"`
}

// EncodeCursor builds the cursor that resumes listing after the given diff.
func EncodeCursor(receiptAt time.Time, cause string) string {
	b, _ := json.Marshal(cursorPayload{ReceiptAt: receiptAt.UTC(), Cause: cause})
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor reads back the diff a cursor resumes after.
func DecodeCursor(cursor string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	var p cursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return time.Time{}, "", err
	}
	return p.ReceiptAt, p.Cause, nil
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
