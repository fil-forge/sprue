// Package postgres provides a PostgreSQL-backed implementation of routingpolicy.Store.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	routingpolicy "github.com/fil-forge/sprue/pkg/store/routing_policy"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const foreignKeyViolation = "23503"

type Store struct {
	pool *pgxpool.Pool
}

var _ routingpolicy.Store = (*Store)(nil)

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Initialize(ctx context.Context) error { return nil }

func (s *Store) SetCandidates(ctx context.Context, policy did.DID, candidates []did.DID, cause cid.Cid) error {
	if len(candidates) == 0 {
		return fmt.Errorf("missing candidates")
	}
	strs := make([]string, 0, len(candidates))
	for _, c := range candidates {
		strs = append(strs, c.String())
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO routing_policy (policy, candidates, cause)
		VALUES ($1, $2, $3)
		ON CONFLICT (policy) DO UPDATE
		SET candidates = EXCLUDED.candidates,
		    cause = EXCLUDED.cause,
		    updated_at = NOW()
	`, policy.String(), strs, cause.String())
	if err != nil {
		return fmt.Errorf("storing routing policy: %w", err)
	}
	return nil
}

func (s *Store) GetPolicy(ctx context.Context, policy did.DID) (routingpolicy.PolicyRecord, error) {
	var (
		policyStr  string
		candidates []string
		causeStr   string
		insertedAt time.Time
		updatedAt  time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT policy, candidates, cause, inserted_at, updated_at
		FROM routing_policy
		WHERE policy = $1
	`, policy.String()).Scan(&policyStr, &candidates, &causeStr, &insertedAt, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return routingpolicy.PolicyRecord{}, routingpolicy.ErrPolicyNotFound
	}
	if err != nil {
		return routingpolicy.PolicyRecord{}, fmt.Errorf("getting routing policy: %w", err)
	}
	policyDID, err := did.Parse(policyStr)
	if err != nil {
		return routingpolicy.PolicyRecord{}, fmt.Errorf("parsing policy DID: %w", err)
	}
	dids := make([]did.DID, 0, len(candidates))
	for _, c := range candidates {
		d, err := did.Parse(c)
		if err != nil {
			return routingpolicy.PolicyRecord{}, fmt.Errorf("parsing candidate DID: %w", err)
		}
		dids = append(dids, d)
	}
	cause, err := cid.Parse(causeStr)
	if err != nil {
		return routingpolicy.PolicyRecord{}, fmt.Errorf("parsing cause CID: %w", err)
	}
	return routingpolicy.PolicyRecord{
		Policy:     policyDID,
		Candidates: dids,
		Cause:      cause,
		InsertedAt: insertedAt,
		UpdatedAt:  updatedAt,
	}, nil
}

func (s *Store) SetSpacePolicy(ctx context.Context, space did.DID, policy did.DID, cause cid.Cid) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO space_routing (space, policy, cause)
		VALUES ($1, $2, $3)
		ON CONFLICT (space) DO UPDATE
		SET policy = EXCLUDED.policy,
		    cause = EXCLUDED.cause,
		    updated_at = NOW()
	`, space.String(), policy.String(), cause.String())
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return routingpolicy.ErrPolicyNotFound
		}
		return fmt.Errorf("storing space routing policy: %w", err)
	}
	return nil
}

func (s *Store) ClearSpacePolicy(ctx context.Context, space did.DID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM space_routing WHERE space = $1`, space.String())
	if err != nil {
		return fmt.Errorf("clearing space routing policy: %w", err)
	}
	return nil
}

func (s *Store) GetSpacePolicy(ctx context.Context, space did.DID) (routingpolicy.SpaceRecord, error) {
	var (
		spaceStr   string
		policyStr  string
		causeStr   string
		insertedAt time.Time
		updatedAt  time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT space, policy, cause, inserted_at, updated_at
		FROM space_routing
		WHERE space = $1
	`, space.String()).Scan(&spaceStr, &policyStr, &causeStr, &insertedAt, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return routingpolicy.SpaceRecord{}, routingpolicy.ErrSpacePolicyNotFound
	}
	if err != nil {
		return routingpolicy.SpaceRecord{}, fmt.Errorf("getting space routing policy: %w", err)
	}
	spaceDID, err := did.Parse(spaceStr)
	if err != nil {
		return routingpolicy.SpaceRecord{}, fmt.Errorf("parsing space DID: %w", err)
	}
	policyDID, err := did.Parse(policyStr)
	if err != nil {
		return routingpolicy.SpaceRecord{}, fmt.Errorf("parsing policy DID: %w", err)
	}
	cause, err := cid.Parse(causeStr)
	if err != nil {
		return routingpolicy.SpaceRecord{}, fmt.Errorf("parsing cause CID: %w", err)
	}
	return routingpolicy.SpaceRecord{
		Space:      spaceDID,
		Policy:     policyDID,
		Cause:      cause,
		InsertedAt: insertedAt,
		UpdatedAt:  updatedAt,
	}, nil
}
