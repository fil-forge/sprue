// Package postgres provides a PostgreSQL-backed implementation of consumer.Store.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/consumer"
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

var _ consumer.Store = (*Store)(nil)

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Initialize(ctx context.Context) error { return nil }

func (s *Store) Add(ctx context.Context, provider did.DID, space did.DID, customer did.DID, subscription string, cause cid.Cid) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO consumer (subscription, provider, consumer, customer, cause, inserted_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, subscription, provider.String(), space.String(), customer.String(), cause.String(), time.Now().UTC())
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return consumer.ErrConsumerExists
		}
		return fmt.Errorf("adding consumer: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, provider did.DID, space did.DID) (consumer.Record, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT subscription, provider, consumer, customer, cause
		FROM consumer
		WHERE consumer = $1 AND provider = $2
		LIMIT 1
	`, space.String(), provider.String())
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return consumer.Record{}, consumer.ErrConsumerNotFound
	}
	if err != nil {
		return consumer.Record{}, fmt.Errorf("getting consumer: %w", err)
	}
	return rec, nil
}

func (s *Store) GetBySubscription(ctx context.Context, provider did.DID, subscription string) (consumer.Record, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT subscription, provider, consumer, customer, cause
		FROM consumer
		WHERE subscription = $1 AND provider = $2
	`, subscription, provider.String())
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return consumer.Record{}, consumer.ErrConsumerNotFound
	}
	if err != nil {
		return consumer.Record{}, fmt.Errorf("getting consumer by subscription: %w", err)
	}
	return rec, nil
}

func (s *Store) List(ctx context.Context, space did.DID, options ...consumer.ListOption) (store.Page[consumer.Record], error) {
	cfg := consumer.ListConfig{}
	for _, opt := range options {
		opt(&cfg)
	}
	return s.listByColumn(ctx, "consumer", space.String(), cfg.Cursor, cfg.Limit)
}

func (s *Store) ListByCustomer(ctx context.Context, customer did.DID, options ...consumer.ListByCustomerOption) (store.Page[consumer.Record], error) {
	cfg := consumer.ListByCustomerConfig{}
	for _, opt := range options {
		opt(&cfg)
	}
	return s.listByColumn(ctx, "customer", customer.String(), cfg.Cursor, cfg.Limit)
}

func (s *Store) listByColumn(ctx context.Context, column, value string, cursor *string, limitPtr *int) (store.Page[consumer.Record], error) {
	limit := defaultListLimit
	if limitPtr != nil && *limitPtr > 0 {
		limit = *limitPtr
	}

	args := []any{value, limit + 1}
	query := fmt.Sprintf(`
		SELECT subscription, provider, consumer, customer, cause
		FROM consumer
		WHERE %s = $1
	`, column)
	if cursor != nil {
		provider, subscription, err := splitCursor(*cursor)
		if err != nil {
			return store.Page[consumer.Record]{}, fmt.Errorf("invalid cursor: %w", err)
		}
		args = append(args, provider, subscription)
		query += ` AND (provider, subscription) > ($3, $4)`
	}
	query += ` ORDER BY provider, subscription LIMIT $2`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return store.Page[consumer.Record]{}, fmt.Errorf("listing consumers: %w", err)
	}
	defer rows.Close()

	records := make([]consumer.Record, 0, limit)
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return store.Page[consumer.Record]{}, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return store.Page[consumer.Record]{}, fmt.Errorf("listing consumers: %w", err)
	}

	var cursorOut *string
	if len(records) > limit {
		last := records[limit-1]
		c := joinCursor(last.Provider.String(), last.Subscription)
		cursorOut = &c
		records = records[:limit]
	}
	return store.Page[consumer.Record]{Results: records, Cursor: cursorOut}, nil
}

func joinCursor(provider, subscription string) string {
	return provider + "|" + subscription
}

func splitCursor(cursor string) (provider, subscription string, err error) {
	parts := strings.SplitN(cursor, "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("malformed cursor %q", cursor)
	}
	return parts[0], parts[1], nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecord(row rowScanner) (consumer.Record, error) {
	var subscription, providerStr, consumerStr, customerStr, causeStr string
	if err := row.Scan(&subscription, &providerStr, &consumerStr, &customerStr, &causeStr); err != nil {
		return consumer.Record{}, err
	}
	provider, err := did.Parse(providerStr)
	if err != nil {
		return consumer.Record{}, fmt.Errorf("parsing provider DID: %w", err)
	}
	space, err := did.Parse(consumerStr)
	if err != nil {
		return consumer.Record{}, fmt.Errorf("parsing consumer DID: %w", err)
	}
	customerDID, err := did.Parse(customerStr)
	if err != nil {
		return consumer.Record{}, fmt.Errorf("parsing customer DID: %w", err)
	}
	cause, err := cid.Parse(causeStr)
	if err != nil {
		return consumer.Record{}, fmt.Errorf("parsing cause CID: %w", err)
	}
	return consumer.Record{
		Subscription: subscription,
		Provider:     provider,
		Consumer:     space,
		Customer:     customerDID,
		Cause:        cause,
	}, nil
}
