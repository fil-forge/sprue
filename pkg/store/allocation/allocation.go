// Package allocation tracks blobs that have been allocated on storage nodes
// but not necessarily accepted, so that blobs can be billed on allocation
// rather than acceptance. Records are added after a successful /blob/allocate
// and removed by /blob/remove (accepted blobs) or /blob/abort (never-accepted
// blobs).
package allocation

import (
	"context"
	"time"

	"github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
)

const (
	EntryNotFoundErrorName = "EntryNotFound"
	EntryExistsErrorName   = "EntryExists"
)

var (
	// ErrEntryNotFound indicates an entry was not found that matches the passed details.
	ErrEntryNotFound = errors.New(EntryNotFoundErrorName, "allocation not found")
	// ErrEntryExists indicates an entry already exists that matches the passed details.
	ErrEntryExists = errors.New(EntryExistsErrorName, "allocation already exists")
)

type (
	ListConfig = store.PaginationConfig
	ListOption func(cfg *ListConfig)
	Blob       = blob.Blob
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

type Record struct {
	Space      did.DID
	Blob       Blob
	Cause      cid.Cid
	InsertedAt time.Time
}

type Store interface {
	// Add records an allocation if one does not already exist. May return
	// [ErrEntryExists] if the blob is already allocated in the space.
	Add(ctx context.Context, space did.DID, blob Blob, cause cid.Cid) error
	// Get looks up an existing allocation. May return [ErrEntryNotFound].
	Get(ctx context.Context, space did.DID, digest multihash.Multihash) (Record, error)
	// List enumerates allocations for a given space.
	List(ctx context.Context, space did.DID, options ...ListOption) (store.Page[Record], error)
	// Remove deletes an allocation if it exists. May return
	// [ErrEntryNotFound]. The cause is the task link of the invocation
	// performing the removal.
	Remove(ctx context.Context, space did.DID, digest multihash.Multihash, cause cid.Cid) error
}
