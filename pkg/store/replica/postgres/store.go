// Package postgres provides a PostgreSQL-backed implementation of replica.Store.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fil-forge/go-libstoracha/digestutil"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/store/replica"
	"github.com/ipfs/go-cid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multiformats/go-multihash"
)

const uniqueViolation = "23505"

type Store struct {
	pool *pgxpool.Pool
}

var _ replica.Store = (*Store)(nil)

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Initialize(ctx context.Context) error { return nil }

func (s *Store) Add(ctx context.Context, space did.DID, digest multihash.Multihash, provider did.DID, status replica.ReplicationStatus, cause cid.Cid) error {
	now := time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO replica (space, digest, provider, status, cause, inserted_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
	`, space.String(), digestutil.Format(digest), provider.String(), status.String(), cause.String(), now)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return replica.ErrReplicaExists
		}
		return fmt.Errorf("adding replica: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, space did.DID, digest multihash.Multihash) ([]replica.Record, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT space, digest, provider, status, cause, inserted_at, updated_at
		FROM replica
		WHERE space = $1 AND digest = $2
		ORDER BY provider ASC
	`, space.String(), digestutil.Format(digest))
	if err != nil {
		return nil, fmt.Errorf("listing replicas: %w", err)
	}
	defer rows.Close()

	var records []replica.Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating replicas: %w", err)
	}
	return records, nil
}

func (s *Store) Retry(ctx context.Context, space did.DID, digest multihash.Multihash, provider did.DID, status replica.ReplicationStatus, cause cid.Cid) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE replica
		SET status = $1, cause = $2, updated_at = $3
		WHERE space = $4 AND digest = $5 AND provider = $6
	`, status.String(), cause.String(), time.Now().UTC(), space.String(), digestutil.Format(digest), provider.String())
	if err != nil {
		return fmt.Errorf("retrying replica: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return replica.ErrReplicaNotFound
	}
	return nil
}

func (s *Store) SetStatus(ctx context.Context, space did.DID, digest multihash.Multihash, provider did.DID, status replica.ReplicationStatus) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE replica
		SET status = $1, updated_at = $2
		WHERE space = $3 AND digest = $4 AND provider = $5
	`, status.String(), time.Now().UTC(), space.String(), digestutil.Format(digest), provider.String())
	if err != nil {
		return fmt.Errorf("setting replica status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return replica.ErrReplicaNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecord(row rowScanner) (replica.Record, error) {
	var (
		spaceStr    string
		digestStr   string
		providerStr string
		statusStr   string
		causeStr    string
		insertedAt  time.Time
		updatedAt   time.Time
	)
	if err := row.Scan(&spaceStr, &digestStr, &providerStr, &statusStr, &causeStr, &insertedAt, &updatedAt); err != nil {
		return replica.Record{}, err
	}
	space, err := did.Parse(spaceStr)
	if err != nil {
		return replica.Record{}, fmt.Errorf("parsing space DID: %w", err)
	}
	digest, err := digestutil.Parse(digestStr)
	if err != nil {
		return replica.Record{}, fmt.Errorf("parsing digest: %w", err)
	}
	provider, err := did.Parse(providerStr)
	if err != nil {
		return replica.Record{}, fmt.Errorf("parsing provider DID: %w", err)
	}
	status, err := parseStatus(statusStr)
	if err != nil {
		return replica.Record{}, err
	}
	cause, err := cid.Parse(causeStr)
	if err != nil {
		return replica.Record{}, fmt.Errorf("parsing cause CID: %w", err)
	}
	return replica.Record{
		Space:     space,
		Digest:    digest,
		Provider:  provider,
		Status:    status,
		Cause:     cause,
		CreatedAt: insertedAt,
		UpdatedAt: updatedAt,
	}, nil
}

func parseStatus(s string) (replica.ReplicationStatus, error) {
	switch s {
	case replica.Allocated.String():
		return replica.Allocated, nil
	case replica.Transferred.String():
		return replica.Transferred, nil
	case replica.Failed.String():
		return replica.Failed, nil
	}
	return 0, fmt.Errorf("unknown replication status %q", s)
}
