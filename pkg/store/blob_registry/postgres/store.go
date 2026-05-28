// Package postgres provides a PostgreSQL-backed implementation of
// blob_registry.Store. Register and Deregister coordinate writes to
// blob_registry, space_diff, and the metrics stores in a single transaction.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fil-forge/libforge/digestutil"
	"github.com/fil-forge/sprue/pkg/store"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	pgmetrics "github.com/fil-forge/sprue/pkg/store/metrics/postgres"
	pgspacediff "github.com/fil-forge/sprue/pkg/store/space_diff/postgres"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multiformats/go-multihash"
)

const (
	defaultListLimit = 1000
	uniqueViolation  = "23505"
)

type Store struct {
	pool          *pgxpool.Pool
	consumerStore consumer.Store
}

var _ blobregistry.Store = (*Store)(nil)

// New returns a Postgres-backed blob registry store. The consumerStore is used
// to fetch subscriptions for space_diff writes; the metrics and space_diff
// writes flow through package-level helpers from the metrics/postgres and
// space_diff/postgres packages.
func New(pool *pgxpool.Pool, consumerStore consumer.Store) *Store {
	return &Store{pool: pool, consumerStore: consumerStore}
}

func (s *Store) Initialize(ctx context.Context) error { return nil }

func (s *Store) Get(ctx context.Context, space did.DID, digest multihash.Multihash) (blobregistry.Record, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT space, digest, size, cause, inserted_at
		FROM blob_registry
		WHERE space = $1 AND digest = $2
	`, space.String(), digestutil.Format(digest))
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return blobregistry.Record{}, blobregistry.ErrEntryNotFound
	}
	if err != nil {
		return blobregistry.Record{}, fmt.Errorf("getting blob registry entry: %w", err)
	}
	return rec, nil
}

func (s *Store) Register(ctx context.Context, space did.DID, blob blobregistry.Blob, cause cid.Cid) error {
	consumers, err := s.collectConsumers(ctx, space)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO blob_registry (space, digest, size, cause, inserted_at)
		VALUES ($1, $2, $3, $4, $5)
	`, space.String(), digestutil.Format(blob.Digest), int64(blob.Size), cause.String(), time.Now().UTC()); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return blobregistry.ErrEntryExists
		}
		return fmt.Errorf("inserting blob registry entry: %w", err)
	}

	receiptAt := time.Now()
	for _, c := range consumers {
		if err := pgspacediff.PutWith(ctx, tx, c.Provider, space, c.Subscription, cause, int64(blob.Size), receiptAt); err != nil {
			return err
		}
	}

	inc := map[string]uint64{
		metrics.BlobAddTotalMetric:     1,
		metrics.BlobAddSizeTotalMetric: blob.Size,
	}
	if err := pgmetrics.IncrementSpaceWith(ctx, tx, space, inc); err != nil {
		return err
	}
	if err := pgmetrics.IncrementAdminWith(ctx, tx, inc); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing blob register: %w", err)
	}
	return nil
}

func (s *Store) Deregister(ctx context.Context, space did.DID, digest multihash.Multihash, cause cid.Cid) error {
	existing, err := s.Get(ctx, space, digest)
	if err != nil {
		return err
	}

	consumers, err := s.collectConsumers(ctx, space)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		DELETE FROM blob_registry WHERE space = $1 AND digest = $2
	`, space.String(), digestutil.Format(digest))
	if err != nil {
		return fmt.Errorf("deleting blob registry entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return blobregistry.ErrEntryNotFound
	}

	receiptAt := time.Now()
	for _, c := range consumers {
		if err := pgspacediff.PutWith(ctx, tx, c.Provider, space, c.Subscription, cause, -int64(existing.Blob.Size), receiptAt); err != nil {
			return err
		}
	}

	inc := map[string]uint64{
		metrics.BlobRemoveTotalMetric:     1,
		metrics.BlobRemoveSizeTotalMetric: existing.Blob.Size,
	}
	if err := pgmetrics.IncrementSpaceWith(ctx, tx, space, inc); err != nil {
		return err
	}
	if err := pgmetrics.IncrementAdminWith(ctx, tx, inc); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing blob deregister: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, space did.DID, options ...blobregistry.ListOption) (store.Page[blobregistry.Record], error) {
	cfg := blobregistry.ListConfig{}
	for _, o := range options {
		o(&cfg)
	}
	limit := defaultListLimit
	if cfg.Limit != nil && *cfg.Limit > 0 {
		limit = *cfg.Limit
	}

	args := []any{space.String(), limit + 1}
	query := `
		SELECT space, digest, size, cause, inserted_at
		FROM blob_registry
		WHERE space = $1
	`
	if cfg.Cursor != nil {
		args = append(args, *cfg.Cursor)
		query += ` AND digest > $3`
	}
	query += ` ORDER BY digest ASC LIMIT $2`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return store.Page[blobregistry.Record]{}, fmt.Errorf("listing blob registry entries: %w", err)
	}
	defer rows.Close()

	records := make([]blobregistry.Record, 0, limit)
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return store.Page[blobregistry.Record]{}, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return store.Page[blobregistry.Record]{}, fmt.Errorf("iterating blob registry entries: %w", err)
	}

	var cursor *string
	if len(records) > limit {
		last := digestutil.Format(records[limit-1].Blob.Digest)
		cursor = &last
		records = records[:limit]
	}
	return store.Page[blobregistry.Record]{Results: records, Cursor: cursor}, nil
}

func (s *Store) collectConsumers(ctx context.Context, space did.DID) ([]consumer.Record, error) {
	results, err := store.Collect(ctx, func(ctx context.Context, options store.PaginationConfig) (store.Page[consumer.Record], error) {
		opts := []consumer.ListOption{}
		if options.Cursor != nil {
			opts = append(opts, consumer.WithListCursor(*options.Cursor))
		}
		return s.consumerStore.List(ctx, space, opts...)
	})
	if err != nil {
		return nil, fmt.Errorf("listing consumers: %w", err)
	}
	if len(results) == 0 {
		return nil, consumer.ErrConsumerNotFound
	}
	return results, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecord(row rowScanner) (blobregistry.Record, error) {
	var (
		spaceStr   string
		digestStr  string
		size       int64
		causeStr   string
		insertedAt time.Time
	)
	if err := row.Scan(&spaceStr, &digestStr, &size, &causeStr, &insertedAt); err != nil {
		return blobregistry.Record{}, err
	}
	space, err := did.Parse(spaceStr)
	if err != nil {
		return blobregistry.Record{}, fmt.Errorf("parsing space DID: %w", err)
	}
	digest, err := digestutil.Parse(digestStr)
	if err != nil {
		return blobregistry.Record{}, fmt.Errorf("parsing digest: %w", err)
	}
	cause, err := cid.Parse(causeStr)
	if err != nil {
		return blobregistry.Record{}, fmt.Errorf("parsing cause CID: %w", err)
	}
	return blobregistry.Record{
		Space: space,
		Blob: blobregistry.Blob{
			Digest: digest,
			Size:   uint64(size),
		},
		Cause:      cause,
		InsertedAt: insertedAt,
	}, nil
}
