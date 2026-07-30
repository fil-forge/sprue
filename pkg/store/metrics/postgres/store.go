// Package postgres provides PostgreSQL-backed implementations of metrics.Store
// and metrics.SpaceStore.
package postgres

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/fil-forge/sprue/pkg/store/metrics"
	"github.com/fil-forge/ucantone/did"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxExec is the common Exec surface shared by *pgxpool.Pool and pgx.Tx.
type pgxExec interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Store persists global admin metrics in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

var _ metrics.Store = (*Store)(nil)

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Initialize(ctx context.Context) error { return nil }

func (s *Store) Get(ctx context.Context) (map[string]uint64, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, value FROM admin_metrics`)
	if err != nil {
		return nil, fmt.Errorf("querying admin metrics: %w", err)
	}
	defer rows.Close()
	result := map[string]uint64{}
	for rows.Next() {
		var name string
		var value int64
		if err := rows.Scan(&name, &value); err != nil {
			return nil, fmt.Errorf("scanning admin metrics: %w", err)
		}
		result[name] = uint64(value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating admin metrics: %w", err)
	}
	return result, nil
}

func (s *Store) IncrementTotals(ctx context.Context, inc map[string]uint64) error {
	if len(inc) == 0 {
		return nil
	}
	return IncrementAdminWith(ctx, s.pool, inc)
}

// IncrementAdminWith increments the admin metrics via the provided querier,
// enabling inclusion in an external transaction.
func IncrementAdminWith(ctx context.Context, q pgxExec, inc map[string]uint64) error {
	// Upsert in a deterministic key order so concurrent transactions acquire the
	// metric row locks in the same order — ranging the map directly would use
	// Go's randomized iteration order and can deadlock (SQLSTATE 40P01).
	for _, metric := range slices.Sorted(maps.Keys(inc)) {
		if _, err := q.Exec(ctx, `
			INSERT INTO admin_metrics (name, value)
			VALUES ($1, $2)
			ON CONFLICT (name) DO UPDATE SET value = admin_metrics.value + EXCLUDED.value
		`, metric, int64(inc[metric])); err != nil {
			return fmt.Errorf("incrementing admin metric %q: %w", metric, err)
		}
	}
	return nil
}

// SpaceStore persists per-space metrics in PostgreSQL.
type SpaceStore struct {
	pool *pgxpool.Pool
}

var _ metrics.SpaceStore = (*SpaceStore)(nil)

func NewSpaceStore(pool *pgxpool.Pool) *SpaceStore {
	return &SpaceStore{pool: pool}
}

func (s *SpaceStore) Initialize(ctx context.Context) error { return nil }

func (s *SpaceStore) Get(ctx context.Context, space did.DID) (map[string]uint64, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, value FROM space_metrics WHERE space = $1`, space.String())
	if err != nil {
		return nil, fmt.Errorf("querying space metrics: %w", err)
	}
	defer rows.Close()
	result := map[string]uint64{}
	for rows.Next() {
		var name string
		var value int64
		if err := rows.Scan(&name, &value); err != nil {
			return nil, fmt.Errorf("scanning space metrics: %w", err)
		}
		result[name] = uint64(value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating space metrics: %w", err)
	}
	return result, nil
}

func (s *SpaceStore) IncrementTotals(ctx context.Context, space did.DID, inc map[string]uint64) error {
	if len(inc) == 0 {
		return nil
	}
	return IncrementSpaceWith(ctx, s.pool, space, inc)
}

// IncrementSpaceWith increments per-space metrics via the provided querier,
// enabling inclusion in an external transaction.
func IncrementSpaceWith(ctx context.Context, q pgxExec, space did.DID, inc map[string]uint64) error {
	// Upsert in a deterministic key order so concurrent transactions acquire the
	// metric row locks in the same order — ranging the map directly would use
	// Go's randomized iteration order and can deadlock (SQLSTATE 40P01).
	for _, metric := range slices.Sorted(maps.Keys(inc)) {
		if _, err := q.Exec(ctx, `
			INSERT INTO space_metrics (space, name, value)
			VALUES ($1, $2, $3)
			ON CONFLICT (space, name) DO UPDATE SET value = space_metrics.value + EXCLUDED.value
		`, space.String(), metric, int64(inc[metric])); err != nil {
			return fmt.Errorf("incrementing space metric %q: %w", metric, err)
		}
	}
	return nil
}
