// Package postgres provides a PostgreSQL-backed implementation of storage_provider.Store.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultListLimit = 1000

type Store struct {
	pool *pgxpool.Pool
}

var _ storageprovider.Store = (*Store)(nil)

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Initialize(ctx context.Context) error { return nil }

func (s *Store) Put(ctx context.Context, id did.DID, endpoint url.URL, weight int, replicationWeight *int, proofs ucan.Container) error {
	if proofs == nil {
		return fmt.Errorf("missing proofs")
	}
	proofBytes, err := container.Encode(container.Raw, proofs)
	if err != nil {
		return fmt.Errorf("encoding proofs: %w", err)
	}
	now := time.Now().UTC()
	_, err = s.pool.Exec(ctx, `
		INSERT INTO storage_provider (provider, endpoint, weight, replication_weight, proofs, inserted_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		ON CONFLICT (provider) DO UPDATE
		SET endpoint = EXCLUDED.endpoint,
		    weight = EXCLUDED.weight,
		    replication_weight = EXCLUDED.replication_weight,
		    proofs = EXCLUDED.proofs,
		    updated_at = EXCLUDED.updated_at
	`, id.String(), endpoint.String(), weight, replicationWeight, proofBytes, now)
	if err != nil {
		return fmt.Errorf("storing storage provider: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, providerID did.DID) (storageprovider.Record, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT provider, endpoint, weight, replication_weight, proofs, inserted_at, updated_at
		FROM storage_provider
		WHERE provider = $1
	`, providerID.String())
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return storageprovider.Record{}, storageprovider.ErrStorageProviderNotFound
	}
	if err != nil {
		return storageprovider.Record{}, fmt.Errorf("getting storage provider: %w", err)
	}
	return rec, nil
}

func (s *Store) Delete(ctx context.Context, providerID did.DID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM storage_provider WHERE provider = $1`, providerID.String())
	if err != nil {
		return fmt.Errorf("deleting storage provider: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storageprovider.ErrStorageProviderNotFound
	}
	return nil
}

func (s *Store) List(ctx context.Context, options ...storageprovider.ListOption) (store.Page[storageprovider.Record], error) {
	cfg := storageprovider.ListConfig{}
	for _, opt := range options {
		opt(&cfg)
	}
	limit := defaultListLimit
	if cfg.Limit != nil && *cfg.Limit > 0 {
		limit = *cfg.Limit
	}

	args := []any{limit + 1}
	query := `
		SELECT provider, endpoint, weight, replication_weight, proofs, inserted_at, updated_at
		FROM storage_provider
	`
	if cfg.Cursor != nil {
		args = append(args, *cfg.Cursor)
		query += ` WHERE provider > $2`
	}
	query += ` ORDER BY provider ASC LIMIT $1`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return store.Page[storageprovider.Record]{}, fmt.Errorf("listing storage providers: %w", err)
	}
	defer rows.Close()

	records := make([]storageprovider.Record, 0, limit)
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return store.Page[storageprovider.Record]{}, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return store.Page[storageprovider.Record]{}, fmt.Errorf("listing storage providers: %w", err)
	}

	var cursor *string
	if len(records) > limit {
		last := records[limit-1].Provider.String()
		cursor = &last
		records = records[:limit]
	}
	return store.Page[storageprovider.Record]{Results: records, Cursor: cursor}, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecord(row rowScanner) (storageprovider.Record, error) {
	var (
		providerStr       string
		endpointStr       string
		weight            int
		replicationWeight *int
		proofBytes        []byte
		insertedAt        time.Time
		updatedAt         time.Time
	)
	if err := row.Scan(&providerStr, &endpointStr, &weight, &replicationWeight, &proofBytes, &insertedAt, &updatedAt); err != nil {
		return storageprovider.Record{}, err
	}
	providerDID, err := did.Parse(providerStr)
	if err != nil {
		return storageprovider.Record{}, fmt.Errorf("parsing provider DID: %w", err)
	}
	endpoint, err := url.Parse(endpointStr)
	if err != nil {
		return storageprovider.Record{}, fmt.Errorf("parsing endpoint URL: %w", err)
	}
	var proofs ucan.Container
	if len(proofBytes) > 0 {
		proofs, err = container.Decode(proofBytes)
		if err != nil {
			return storageprovider.Record{}, fmt.Errorf("decoding proofs: %w", err)
		}
	}
	return storageprovider.Record{
		Provider:          providerDID,
		Endpoint:          *endpoint,
		Weight:            weight,
		ReplicationWeight: replicationWeight,
		Proofs:            proofs,
		InsertedAt:        insertedAt,
		UpdatedAt:         updatedAt,
	}, nil
}
