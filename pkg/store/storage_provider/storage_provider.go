package storageprovider

import (
	"context"
	"net/url"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/ucan"
)

const (
	// StorageProviderNotFoundErrorName is the name given to an error where the
	// storage provider is not found in the store.
	StorageProviderNotFoundErrorName = "StorageProviderNotFound"
)

var (
	ErrStorageProviderNotFound = errors.New(StorageProviderNotFoundErrorName, "storage provider not found")
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

type Record struct {
	// DID of the storage provider.
	Provider did.DID
	// Public URL that accepts UCAN invocations.
	Endpoint url.URL
	// Weight determines chance of selection for uploads relative to other
	// providers.
	Weight int
	// ReplicationWeight determines the chance of selection for replications
	// relative to other providers. Defaults to weight if not set.
	ReplicationWeight *int
	// Proofs are UCAN delegations granting the upload service `/blob/allocate`,
	// `/blob/accept` and `/blob/replica/allocate`.
	Proofs ucan.Container
	// Date and time the record was created (ISO 8601).
	InsertedAt time.Time
	// Date and time the record was last updated (ISO 8601).
	UpdatedAt time.Time
}

type Store interface {
	Put(ctx context.Context, providerID did.DID, endpoint url.URL, weight int, replicationWeight *int, proofs ucan.Container) error
	// Get a storage provider record by provider DID. May return
	// [ErrStorageProviderNotFound].
	Get(ctx context.Context, providerID did.DID) (Record, error)
	// Delete a storage provider record by provider DID. May return
	// [ErrStorageProviderNotFound] if the record does not exist.
	Delete(ctx context.Context, providerID did.DID) error
	List(ctx context.Context, options ...ListOption) (store.Page[Record], error)
}
