// Package postgres provides a PostgreSQL-backed implementation of customer.Store.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/customer"
	"github.com/fil-forge/ucantone/did"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultListLimit = 1000
	uniqueViolation  = "23505"
)

// Store persists customer records in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

var _ customer.Store = (*Store)(nil)

// New returns a Postgres-backed customer store.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Initialize is a no-op. Schema is managed by the shared goose migrations.
func (s *Store) Initialize(ctx context.Context) error { return nil }

func (s *Store) Get(ctx context.Context, customerID did.DID) (customer.Record, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT customer, external_account, product, details, reserved_capacity, inserted_at, updated_at
		FROM customer
		WHERE customer = $1
	`, customerID.String())
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return customer.Record{}, customer.ErrCustomerNotFound
	}
	if err != nil {
		return customer.Record{}, fmt.Errorf("getting customer: %w", err)
	}
	return rec, nil
}

func (s *Store) Add(ctx context.Context, customerID did.DID, externalAccount *string, product did.DID, details map[string]any, reservedCapacity *uint64) error {
	var detailsJSON []byte
	if len(details) > 0 {
		b, err := json.Marshal(details)
		if err != nil {
			return fmt.Errorf("marshalling details: %w", err)
		}
		detailsJSON = b
	}

	var capacity *int64
	if reservedCapacity != nil {
		c := int64(*reservedCapacity)
		capacity = &c
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO customer (customer, external_account, product, details, reserved_capacity, inserted_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, customerID.String(), externalAccount, product.String(), detailsJSON, capacity, time.Now().UTC())

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return customer.ErrCustomerExists
		}
		return fmt.Errorf("adding customer: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, options ...customer.ListOption) (store.Page[customer.Record], error) {
	cfg := customer.ListConfig{}
	for _, opt := range options {
		opt(&cfg)
	}

	limit := defaultListLimit
	if cfg.Limit != nil && *cfg.Limit > 0 {
		limit = *cfg.Limit
	}

	args := []any{limit + 1}
	query := `
		SELECT customer, external_account, product, details, reserved_capacity, inserted_at, updated_at
		FROM customer
	`
	if cfg.Cursor != nil {
		args = append(args, *cfg.Cursor)
		query += ` WHERE customer > $2`
	}
	query += ` ORDER BY customer ASC LIMIT $1`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return store.Page[customer.Record]{}, fmt.Errorf("listing customers: %w", err)
	}
	defer rows.Close()

	records := make([]customer.Record, 0, limit)
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return store.Page[customer.Record]{}, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return store.Page[customer.Record]{}, fmt.Errorf("listing customers: %w", err)
	}

	var cursor *string
	if len(records) > limit {
		last := records[limit-1].Customer.String()
		cursor = &last
		records = records[:limit]
	}
	return store.Page[customer.Record]{Results: records, Cursor: cursor}, nil
}

func (s *Store) UpdateProduct(ctx context.Context, customerID did.DID, product did.DID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE customer
		SET product = $1, updated_at = $2
		WHERE customer = $3
	`, product.String(), time.Now().UTC(), customerID.String())
	if err != nil {
		return fmt.Errorf("updating customer product: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return customer.ErrCustomerNotFound
	}
	return nil
}

// rowScanner abstracts pgx.Row and pgx.Rows for scanRecord.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecord(row rowScanner) (customer.Record, error) {
	var (
		customerStr     string
		externalAccount *string
		productStr      string
		detailsJSON     []byte
		capacity        *int64
		insertedAt      time.Time
		updatedAtRaw    *time.Time
	)
	if err := row.Scan(&customerStr, &externalAccount, &productStr, &detailsJSON, &capacity, &insertedAt, &updatedAtRaw); err != nil {
		return customer.Record{}, err
	}

	customerID, err := did.Parse(customerStr)
	if err != nil {
		return customer.Record{}, fmt.Errorf("parsing customer DID: %w", err)
	}
	product, err := did.Parse(productStr)
	if err != nil {
		return customer.Record{}, fmt.Errorf("parsing product DID: %w", err)
	}

	rec := customer.Record{
		Customer:        customerID,
		ExternalAccount: externalAccount,
		Product:         product,
		InsertedAt:      insertedAt,
	}
	if updatedAtRaw != nil {
		rec.UpdatedAt = *updatedAtRaw
	}
	if capacity != nil {
		c := uint64(*capacity)
		rec.ReservedCapacity = &c
	}
	if len(detailsJSON) > 0 {
		var details map[string]any
		if err := json.Unmarshal(detailsJSON, &details); err != nil {
			return customer.Record{}, fmt.Errorf("unmarshalling details: %w", err)
		}
		rec.Details = details
	}
	return rec, nil
}
