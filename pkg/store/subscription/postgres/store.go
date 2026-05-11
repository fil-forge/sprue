// Package postgres provides a PostgreSQL-backed implementation of subscription.Store.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/subscription"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultListLimit = 1000
	uniqueViolation  = "23505"
)

type Store struct {
	pool *pgxpool.Pool
}

var _ subscription.Store = (*Store)(nil)

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Initialize(ctx context.Context) error { return nil }

func (s *Store) Add(ctx context.Context, provider did.DID, subscriptionID string, customer did.DID, cause cid.Cid) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO subscription (subscription, provider, customer, cause, inserted_at)
		VALUES ($1, $2, $3, $4, $5)
	`, subscriptionID, provider.String(), customer.String(), cause.String(), time.Now().UTC())
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return subscription.ErrSubscriptionExists
		}
		return fmt.Errorf("adding subscription: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, provider did.DID, subscriptionID string) (subscription.Record, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT subscription, provider, customer, cause, inserted_at
		FROM subscription
		WHERE subscription = $1 AND provider = $2
	`, subscriptionID, provider.String())
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return subscription.Record{}, subscription.ErrSubscriptionNotFound
	}
	if err != nil {
		return subscription.Record{}, fmt.Errorf("getting subscription: %w", err)
	}
	return rec, nil
}

func (s *Store) ListByProviderAndCustomer(ctx context.Context, provider did.DID, customer did.DID, options ...subscription.ListByProviderAndCustomerOption) (store.Page[subscription.Record], error) {
	cfg := subscription.ListByProviderAndCustomerConfig{}
	for _, opt := range options {
		opt(&cfg)
	}
	limit := defaultListLimit
	if cfg.Limit != nil && *cfg.Limit > 0 {
		limit = *cfg.Limit
	}

	args := []any{customer.String(), provider.String(), limit + 1}
	query := `
		SELECT subscription, provider, customer, cause, inserted_at
		FROM subscription
		WHERE customer = $1 AND provider = $2
	`
	if cfg.Cursor != nil {
		args = append(args, *cfg.Cursor)
		query += ` AND subscription > $4`
	}
	query += ` ORDER BY subscription ASC LIMIT $3`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return store.Page[subscription.Record]{}, fmt.Errorf("listing subscriptions: %w", err)
	}
	defer rows.Close()

	records := make([]subscription.Record, 0, limit)
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return store.Page[subscription.Record]{}, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return store.Page[subscription.Record]{}, fmt.Errorf("listing subscriptions: %w", err)
	}

	var cursor *string
	if len(records) > limit {
		last := records[limit-1].Subscription
		cursor = &last
		records = records[:limit]
	}
	return store.Page[subscription.Record]{Results: records, Cursor: cursor}, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecord(row rowScanner) (subscription.Record, error) {
	var subscriptionID, providerStr, customerStr, causeStr string
	var insertedAt time.Time
	if err := row.Scan(&subscriptionID, &providerStr, &customerStr, &causeStr, &insertedAt); err != nil {
		return subscription.Record{}, err
	}
	provider, err := did.Parse(providerStr)
	if err != nil {
		return subscription.Record{}, fmt.Errorf("parsing provider DID: %w", err)
	}
	customer, err := did.Parse(customerStr)
	if err != nil {
		return subscription.Record{}, fmt.Errorf("parsing customer DID: %w", err)
	}
	cause, err := cid.Parse(causeStr)
	if err != nil {
		return subscription.Record{}, fmt.Errorf("parsing cause CID: %w", err)
	}
	return subscription.Record{
		Subscription: subscriptionID,
		Provider:     provider,
		Customer:     customer,
		Cause:        cause,
		InsertedAt:   insertedAt,
	}, nil
}
