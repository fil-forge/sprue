// Package postgres provides a PostgreSQL-backed implementation of revocation.Store.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/fil-forge/sprue/pkg/store/revocation"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

var _ revocation.Store = (*Store)(nil)

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Initialize(ctx context.Context) error { return nil }

// Add inserts a revocation if one does not already exist for the given
// (delegation, scope). Matches the AWS semantics of "leave existing unchanged".
func (s *Store) Add(ctx context.Context, delegation cid.Cid, scope did.DID, cause cid.Cid) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO revocation (revoke, scope, cause, inserted_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (revoke, scope) DO NOTHING
	`, delegation.String(), scope.String(), cause.String(), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("adding revocation: %w", err)
	}
	return nil
}

// Reset replaces all existing revocations for the given delegation with a
// single revocation for the given scope.
func (s *Store) Reset(ctx context.Context, delegation cid.Cid, scope did.DID, cause cid.Cid) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM revocation WHERE revoke = $1`, delegation.String()); err != nil {
		return fmt.Errorf("clearing revocation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO revocation (revoke, scope, cause, inserted_at)
		VALUES ($1, $2, $3, $4)
	`, delegation.String(), scope.String(), cause.String(), time.Now().UTC()); err != nil {
		return fmt.Errorf("inserting revocation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing revocation reset: %w", err)
	}
	return nil
}

func (s *Store) Find(ctx context.Context, delegations []cid.Cid) (map[cid.Cid]map[did.DID]cid.Cid, error) {
	result := map[cid.Cid]map[did.DID]cid.Cid{}
	if len(delegations) == 0 {
		return result, nil
	}
	dlgStrs := make([]string, len(delegations))
	for i, d := range delegations {
		dlgStrs[i] = d.String()
	}

	rows, err := s.pool.Query(ctx, `
		SELECT revoke, scope, cause
		FROM revocation
		WHERE revoke = ANY($1)
	`, dlgStrs)
	if err != nil {
		return nil, fmt.Errorf("finding revocations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var revokeStr, scopeStr, causeStr string
		if err := rows.Scan(&revokeStr, &scopeStr, &causeStr); err != nil {
			return nil, fmt.Errorf("scanning revocation: %w", err)
		}
		dlgCID, err := cid.Parse(revokeStr)
		if err != nil {
			return nil, fmt.Errorf("parsing delegation CID: %w", err)
		}
		scopeDID, err := did.Parse(scopeStr)
		if err != nil {
			return nil, fmt.Errorf("parsing scope DID: %w", err)
		}
		cause, err := cid.Parse(causeStr)
		if err != nil {
			return nil, fmt.Errorf("parsing cause CID: %w", err)
		}
		scopes, ok := result[dlgCID]
		if !ok {
			scopes = map[did.DID]cid.Cid{}
			result[dlgCID] = scopes
		}
		scopes[scopeDID] = cause
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating revocations: %w", err)
	}
	return result, nil
}
