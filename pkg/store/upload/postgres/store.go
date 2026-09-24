// Package postgres provides a PostgreSQL-backed implementation of upload.Store.
//
// Shards are stored in a dedicated upload_shard table with no size
// restriction.
//
// Upsert and Remove coordinate writes to upload, upload_diff and the metrics
// stores in a single transaction, mirroring blob_registry: the object count a
// space reports is upload-add-total minus upload-remove-total, and the diff log
// carries the same change with a timestamp so the count can be bucketed into
// windows.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	pgmetrics "github.com/fil-forge/sprue/pkg/store/metrics/postgres"
	"github.com/fil-forge/sprue/pkg/store/upload"
	pguploaddiff "github.com/fil-forge/sprue/pkg/store/upload_diff/postgres"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultListLimit      = 1000
	maxShardsPerPage      = 1000
	defaultShardPageLimit = 1000
)

type Store struct {
	pool          *pgxpool.Pool
	consumerStore consumer.Store
}

var _ upload.Store = (*Store)(nil)

// New returns a Postgres-backed upload store. The consumerStore is used to
// fetch subscriptions for upload_diff writes; the metrics and upload_diff
// writes flow through package-level helpers from the metrics/postgres and
// upload_diff/postgres packages.
func New(pool *pgxpool.Pool, consumerStore consumer.Store) *Store {
	return &Store{pool: pool, consumerStore: consumerStore}
}

func (s *Store) Initialize(ctx context.Context) error { return nil }

func (s *Store) Exists(ctx context.Context, space did.DID, root cid.Cid) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM upload WHERE space = $1 AND root = $2
		)
	`, space.String(), root.String()).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking upload existence: %w", err)
	}
	return exists, nil
}

func (s *Store) Get(ctx context.Context, space did.DID, root cid.Cid) (upload.UploadRecord, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT space, root, index, cause, inserted_at, updated_at
		FROM upload
		WHERE space = $1 AND root = $2
	`, space.String(), root.String())
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return upload.UploadRecord{}, upload.ErrUploadNotFound
	}
	if err != nil {
		return upload.UploadRecord{}, fmt.Errorf("getting upload: %w", err)
	}
	return rec, nil
}

func (s *Store) Inspect(ctx context.Context, root cid.Cid) (upload.UploadInspectRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT space FROM upload WHERE root = $1`, root.String())
	if err != nil {
		return upload.UploadInspectRecord{}, fmt.Errorf("inspecting upload: %w", err)
	}
	defer rows.Close()
	var spaces []did.DID
	for rows.Next() {
		var spaceStr string
		if err := rows.Scan(&spaceStr); err != nil {
			return upload.UploadInspectRecord{}, fmt.Errorf("scanning upload inspect row: %w", err)
		}
		spaceDID, err := did.Parse(spaceStr)
		if err != nil {
			return upload.UploadInspectRecord{}, fmt.Errorf("parsing space DID: %w", err)
		}
		spaces = append(spaces, spaceDID)
	}
	if err := rows.Err(); err != nil {
		return upload.UploadInspectRecord{}, fmt.Errorf("iterating upload inspect rows: %w", err)
	}
	return upload.UploadInspectRecord{Spaces: spaces}, nil
}

func (s *Store) List(ctx context.Context, space did.DID, options ...upload.ListOption) (store.Page[upload.UploadRecord], error) {
	cfg := upload.ListConfig{}
	for _, o := range options {
		o(&cfg)
	}
	limit := defaultListLimit
	if cfg.Limit != nil && *cfg.Limit > 0 {
		limit = *cfg.Limit
	}

	args := []any{space.String(), limit + 1}
	query := `
		SELECT space, root, index, cause, inserted_at, updated_at
		FROM upload
		WHERE space = $1
	`
	if cfg.Cursor != nil {
		args = append(args, *cfg.Cursor)
		query += ` AND root > $3`
	}
	query += ` ORDER BY root ASC LIMIT $2`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return store.Page[upload.UploadRecord]{}, fmt.Errorf("listing uploads: %w", err)
	}
	defer rows.Close()

	records := make([]upload.UploadRecord, 0, limit)
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return store.Page[upload.UploadRecord]{}, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return store.Page[upload.UploadRecord]{}, fmt.Errorf("iterating uploads: %w", err)
	}

	var cursor *string
	if len(records) > limit {
		last := records[limit-1].Root.String()
		cursor = &last
		records = records[:limit]
	}
	return store.Page[upload.UploadRecord]{Results: records, Cursor: cursor}, nil
}

func (s *Store) ListShards(ctx context.Context, space did.DID, root cid.Cid, options ...upload.ListShardsOption) (store.Page[cid.Cid], error) {
	cfg := upload.ListShardsConfig{}
	for _, o := range options {
		o(&cfg)
	}
	limit := defaultShardPageLimit
	if cfg.Limit != nil && *cfg.Limit > 0 {
		limit = *cfg.Limit
	}
	if limit > maxShardsPerPage {
		limit = maxShardsPerPage
	}

	args := []any{space.String(), root.String(), limit + 1}
	query := `
		SELECT shard FROM upload_shard
		WHERE space = $1 AND root = $2
	`
	if cfg.Cursor != nil {
		args = append(args, *cfg.Cursor)
		query += ` AND shard > $4`
	}
	query += ` ORDER BY shard ASC LIMIT $3`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return store.Page[cid.Cid]{}, fmt.Errorf("listing shards: %w", err)
	}
	defer rows.Close()

	shards := make([]cid.Cid, 0, limit)
	for rows.Next() {
		var shardStr string
		if err := rows.Scan(&shardStr); err != nil {
			return store.Page[cid.Cid]{}, fmt.Errorf("scanning shard: %w", err)
		}
		shard, err := cid.Parse(shardStr)
		if err != nil {
			return store.Page[cid.Cid]{}, fmt.Errorf("parsing shard CID: %w", err)
		}
		shards = append(shards, shard)
	}
	if err := rows.Err(); err != nil {
		return store.Page[cid.Cid]{}, fmt.Errorf("iterating shards: %w", err)
	}

	var cursor *string
	if len(shards) > limit {
		last := shards[limit-1].String()
		cursor = &last
		shards = shards[:limit]
	}
	return store.Page[cid.Cid]{Results: shards, Cursor: cursor}, nil
}

func (s *Store) Remove(ctx context.Context, space did.DID, root cid.Cid, cause cid.Cid) error {
	// Both reads happen before the transaction opens, as they do in
	// blob_registry.Deregister. Collecting consumers from inside it would have
	// this call hold one pooled connection while waiting for another, which
	// deadlocks on a single-connection pool and under enough concurrent
	// removes on any pool.
	//
	// The existence check comes first so a missing root reports
	// ErrUploadNotFound whatever else is true of the space: the handler turns
	// that one error into idempotent success, and reporting a space's lack of
	// consumers instead would fail a remove of something that was never there.
	exists, err := s.Exists(ctx, space, root)
	if err != nil {
		return err
	}
	if !exists {
		return upload.ErrUploadNotFound
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

	tag, err := tx.Exec(ctx, `DELETE FROM upload WHERE space = $1 AND root = $2`, space.String(), root.String())
	if err != nil {
		return fmt.Errorf("removing upload: %w", err)
	}
	// Nothing was removed, so nothing is counted: another remove won the race
	// between the check above and this delete. The count must not move for it.
	if tag.RowsAffected() == 0 {
		return upload.ErrUploadNotFound
	}

	if err := s.recordDelta(ctx, tx, space, consumers, cause, -1, metrics.UploadRemoveTotalMetric); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing upload remove: %w", err)
	}
	return nil
}

func (s *Store) Upsert(ctx context.Context, space did.DID, root cid.Cid, index *cid.Cid, shards []cid.Cid, cause cid.Cid) error {
	consumers, err := s.collectConsumers(ctx, space)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var indexStr *string
	if index != nil {
		str := index.String()
		indexStr = &str
	}
	// xmax is zero on a row this statement inserted and non-zero on one it
	// updated, which is how the count tells a new upload from a re-add. The
	// capability is an upsert by spec — adding the same root again merges
	// shards and replaces the index — so a client retry must leave the count
	// where it was.
	var inserted bool
	if err := tx.QueryRow(ctx, `
		INSERT INTO upload (space, root, index, cause)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (space, root) DO UPDATE
		SET index = EXCLUDED.index, cause = EXCLUDED.cause, updated_at = NOW()
		RETURNING (xmax = 0)
	`, space.String(), root.String(), indexStr, cause.String()).Scan(&inserted); err != nil {
		return fmt.Errorf("upserting upload: %w", err)
	}

	for _, shard := range shards {
		if _, err := tx.Exec(ctx, `
			INSERT INTO upload_shard (space, root, shard)
			VALUES ($1, $2, $3)
			ON CONFLICT (space, root, shard) DO NOTHING
		`, space.String(), root.String(), shard.String()); err != nil {
			return fmt.Errorf("upserting upload shard: %w", err)
		}
	}

	if inserted {
		if err := s.recordDelta(ctx, tx, space, consumers, cause, 1, metrics.UploadAddTotalMetric); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing upload upsert: %w", err)
	}
	return nil
}

// recordDelta writes one object-count change inside tx: an upload_diff row per
// the space's consumers and the matching metric increment at both the space and
// admin scope. metric is the counter to bump (adds and removes have their own,
// each monotonically increasing); delta is the signed change the diff log
// carries, so the count at a time is the cumulative sum up to it.
func (s *Store) recordDelta(ctx context.Context, tx pgx.Tx, space did.DID, consumers []consumer.Record, cause cid.Cid, delta int64, metric string) error {
	receiptAt := time.Now()
	for _, c := range consumers {
		if err := pguploaddiff.PutWith(ctx, tx, c.Provider, space, c.Subscription, cause, delta, receiptAt); err != nil {
			return err
		}
	}

	inc := map[string]uint64{metric: 1}
	if err := pgmetrics.IncrementSpaceWith(ctx, tx, space, inc); err != nil {
		return err
	}
	return pgmetrics.IncrementAdminWith(ctx, tx, inc)
}

// collectConsumers returns the space's provider/subscription pairs, which the
// upload_diff rows are keyed by. A space with none cannot be billed against, so
// it is an error here — the upload/add handler rejects such a space with
// InsufficientStorage before it ever reaches the store.
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

func scanRecord(row rowScanner) (upload.UploadRecord, error) {
	var (
		spaceStr   string
		rootStr    string
		indexStr   *string
		causeStr   string
		insertedAt time.Time
		updatedAt  time.Time
	)
	if err := row.Scan(&spaceStr, &rootStr, &indexStr, &causeStr, &insertedAt, &updatedAt); err != nil {
		return upload.UploadRecord{}, err
	}
	space, err := did.Parse(spaceStr)
	if err != nil {
		return upload.UploadRecord{}, fmt.Errorf("parsing space DID: %w", err)
	}
	root, err := cid.Parse(rootStr)
	if err != nil {
		return upload.UploadRecord{}, fmt.Errorf("parsing root CID: %w", err)
	}
	var index *cid.Cid
	if indexStr != nil {
		c, err := cid.Parse(*indexStr)
		if err != nil {
			return upload.UploadRecord{}, fmt.Errorf("parsing index CID: %w", err)
		}
		index = &c
	}
	cause, err := cid.Parse(causeStr)
	if err != nil {
		return upload.UploadRecord{}, fmt.Errorf("parsing cause CID: %w", err)
	}
	return upload.UploadRecord{
		Space:      space,
		Root:       root,
		Index:      index,
		Cause:      cause,
		InsertedAt: insertedAt,
		UpdatedAt:  updatedAt,
	}, nil
}
