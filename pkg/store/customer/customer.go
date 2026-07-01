package customer

import (
	"context"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
)

const (
	CustomerNotFoundErrorName = "CustomerNotFound"
	CustomerExistsErrorName   = "CustomerExists"
)

// ErrCustomerNotFound indicates a customer does not exist.
var ErrCustomerNotFound = errors.New(CustomerNotFoundErrorName, "customer not found")

// ErrCustomerExists indicates a customer already exists.
var ErrCustomerExists = errors.New(CustomerExistsErrorName, "customer already exists")

type (
	ListConfig = store.PaginationConfig
	ListOption func(cfg *ListConfig)
)

func WithListLimit(limit int) ListOption {
	return func(cfg *ListConfig) { cfg.Limit = &limit }
}

func WithListCursor(cursor string) ListOption {
	return func(cfg *ListConfig) { cfg.Cursor = &cursor }
}

// Captures information about a customer of the service that may need to be
// billed for storage usage.
type Record struct {
	// DID of the user account e.g. `did:mailto:agent`
	Customer did.DID
	// Opaque identifier representing an account in the payment system
	// e.g. Stripe customer ID (stripe:cus_9s6XKzkNRiz8i3)
	ExternalAccount *string
	// Unique identifier of the product a.k.a plan.
	Product did.DID
	// Misc customer details
	Details map[string]any
	// Reserved capacity in bytes for the customer.
	// Only used in the forge network, where capacity is not given by the product/plan.
	ReservedCapacity *uint64
	// Time the record was added to the database.
	InsertedAt time.Time
	// Time the record was updated in the database. Note: may be nil value if never updated.
	UpdatedAt time.Time
}

type Store interface {
	// May return [ErrCustomerNotFound] if the customer does not exist.
	Get(ctx context.Context, customer did.DID) (Record, error)
	// May return [ErrCustomerExists] if the customer already exists.
	Add(ctx context.Context, customer did.DID, account *string, product did.DID, details map[string]any, reservedCapacity *uint64) error
	List(ctx context.Context, options ...ListOption) (store.Page[Record], error)
	// Update the product (plan) for a customer. May return [ErrCustomerNotFound]
	// if the customer does not exist.
	UpdateProduct(ctx context.Context, customer did.DID, product did.DID) error
}
