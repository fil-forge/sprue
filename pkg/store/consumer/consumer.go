package consumer

import (
	"context"

	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/ipfs/go-cid"
)

const (
	ConsumerNotFoundErrorName = "ConsumerNotFound"
	ConsumerExistsErrorName   = "ConsumerExists"
)

var (
	// ErrConsumerNotFound indicates a consumer was not found that matches the passed details.
	ErrConsumerNotFound = errors.New(ConsumerNotFoundErrorName, "consumer not found")
	// ErrConsumerExists indicates a consumer already exists that matches the passed details.
	ErrConsumerExists = errors.New(ConsumerExistsErrorName, "consumer already exists")
)

type (
	ListConfig           = store.PaginationConfig
	ListOption           func(cfg *ListConfig)
	ListByCustomerConfig = store.PaginationConfig
	ListByCustomerOption func(cfg *ListByCustomerConfig)
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

func WithListByCustomerLimit(limit int) ListByCustomerOption {
	return func(cfg *ListByCustomerConfig) {
		cfg.Limit = &limit
	}
}

func WithListByCustomerCursor(cursor string) ListByCustomerOption {
	return func(cfg *ListByCustomerConfig) {
		cfg.Cursor = &cursor
	}
}

type Record struct {
	/** DID of the provider who provides services for the consumer. */
	Provider did.DID
	/** DID of the consumer (e.g. a space) for whom services have been provisioned. */
	Consumer did.DID
	/** DID of the customer (e.g. an account) who owns the consumer. */
	Customer did.DID
	/** ID of the subscription representing the relationship between the consumer and provider. */
	Subscription string
	/**
	 * CID of the UCAN invocation that created this record.
	 * Note: May be nil - this became a required field after 2023-07-10T23:12:38.000Z.
	 */
	Cause cid.Cid
}

type Store interface {
	// May return [ErrConsumerExists] if a consumer record already exists for the
	// given provider, consumer, customer and subscription.
	Add(ctx context.Context, provider did.DID, space did.DID, customer did.DID, subscription string, cause cid.Cid) error
	// May return [ErrConsumerNotFound] if no consumer record exists for the given
	// provider and consumer.
	Get(ctx context.Context, provider did.DID, space did.DID) (Record, error)
	GetBySubscription(ctx context.Context, provider did.DID, subscription string) (Record, error)
	List(ctx context.Context, space did.DID, options ...ListOption) (store.Page[Record], error)
	ListByCustomer(ctx context.Context, customer did.DID, options ...ListByCustomerOption) (store.Page[Record], error)
}
