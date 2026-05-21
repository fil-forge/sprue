package upload

import (
	"context"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"github.com/ipfs/go-cid"
)

const UploadNotFoundErrorName = "UploadNotFound"

// ErrUploadNotFound indicates an upload does not exist.
var ErrUploadNotFound = errors.New(UploadNotFoundErrorName, "upload not found")

type (
	ListConfig       = store.PaginationConfig
	ListOption       func(cfg *ListConfig)
	ListShardsConfig = store.PaginationConfig
	ListShardsOption func(cfg *ListShardsConfig)
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

func WithListShardsLimit(limit int) ListShardsOption {
	return func(cfg *ListShardsConfig) {
		cfg.Limit = &limit
	}
}

func WithListShardsCursor(cursor string) ListShardsOption {
	return func(cfg *ListShardsConfig) {
		cfg.Cursor = &cursor
	}
}

type UploadRecord struct {
	Space      did.DID
	Root       cid.Cid
	Index      *cid.Cid
	Cause      cid.Cid
	InsertedAt time.Time
	UpdatedAt  time.Time
}

type UploadInspectRecord struct {
	Spaces []did.DID
}

type Store interface {
	Exists(ctx context.Context, space did.DID, root cid.Cid) (bool, error)
	// May return [ErrUploadNotFound] if the upload does not exist.
	Get(ctx context.Context, space did.DID, root cid.Cid) (UploadRecord, error)
	Inspect(ctx context.Context, root cid.Cid) (UploadInspectRecord, error)
	List(ctx context.Context, space did.DID, options ...ListOption) (store.Page[UploadRecord], error)
	// Lists the shards of an upload.
	ListShards(ctx context.Context, space did.DID, root cid.Cid, options ...ListShardsOption) (store.Page[cid.Cid], error)
	// Removes an item from the table but fails if the item does not exist.
	Remove(ctx context.Context, space did.DID, root cid.Cid) error
	// Inserts an item in the table if it does not already exist or updates an
	// existing item if it does exist.
	Upsert(ctx context.Context, space did.DID, root cid.Cid, index *cid.Cid, shards []cid.Cid, cause cid.Cid) error
}
