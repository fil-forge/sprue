// Package postgres provides a PostgreSQL-backed implementation of upload_diff.Store.
package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fil-forge/sprue/pkg/store"
	uploaddiff "github.com/fil-forge/sprue/pkg/store/upload_diff"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultListLimit = 1000

// pgxExec is the common Exec surface shared by *pgxpool.Pool and pgx.Tx.
type pgxExec interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Store struct {
	pool *pgxpool.Pool
}

var _ uploaddiff.Store = (*Store)(nil)

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Initialize(ctx context.Context) error { return nil }

func (s *Store) Put(ctx context.Context, space did.DID, cause cid.Cid, delta int64, receiptAt time.Time) (bool, error) {
	return PutWith(ctx, s.pool, space, cause, delta, receiptAt)
}

// PutWith inserts an upload diff row using the provided querier, allowing the
// write to participate in an external transaction. It exists so the upload
// store can batch upload-diff writes with its own updates in one atomic unit.
func PutWith(ctx context.Context, q pgxExec, space did.DID, cause cid.Cid, delta int64, receiptAt time.Time) (bool, error) {
	// ON CONFLICT DO NOTHING is what makes a replayed cause the no-op the schema
	// describes, rather than a unique violation that fails the caller's whole
	// accounting transaction. The reported flag is how the caller keeps its
	// running total in step: a suppressed insert must not move the counters
	// either, or the total stops matching the log it anchors.
	tag, err := q.Exec(ctx, `
		INSERT INTO upload_diff (space, receipt_at, cause, delta)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (space, receipt_at, cause) DO NOTHING
	`, space.String(), uploaddiff.Instant(receiptAt), cause.String(), delta)
	if err != nil {
		return false, fmt.Errorf("putting upload diff: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) List(ctx context.Context, space did.DID, after time.Time, options ...uploaddiff.ListOption) (store.Page[uploaddiff.DifferenceRecord], error) {
	cfg := uploaddiff.ListConfig{}
	for _, opt := range options {
		opt(&cfg)
	}
	limit := defaultListLimit
	if cfg.Limit != nil && *cfg.Limit > 0 {
		limit = *cfg.Limit
	}

	var (
		conds []string
		args  []any
	)
	args = append(args, space.String())
	conds = append(conds, "space = $1")

	if cfg.Cursor != nil {
		receiptAt, cause, err := store.DecodeCursor(*cfg.Cursor)
		if err != nil {
			return store.Page[uploaddiff.DifferenceRecord]{}, fmt.Errorf("invalid cursor: %w", err)
		}
		args = append(args, receiptAt, cause)
		conds = append(conds, fmt.Sprintf("(receipt_at, cause) > ($%d, $%d)", len(args)-1, len(args)))
	} else if !after.IsZero() {
		args = append(args, after.UTC())
		conds = append(conds, fmt.Sprintf("receipt_at > $%d", len(args)))
	}

	args = append(args, limit+1)
	query := fmt.Sprintf(`
		SELECT space, cause, delta, receipt_at, inserted_at
		FROM upload_diff
		WHERE %s
		ORDER BY receipt_at ASC, cause ASC
		LIMIT $%d
	`, strings.Join(conds, " AND "), len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return store.Page[uploaddiff.DifferenceRecord]{}, fmt.Errorf("listing upload diffs: %w", err)
	}
	defer rows.Close()

	records := make([]uploaddiff.DifferenceRecord, 0, limit)
	for rows.Next() {
		var (
			spaceStr   string
			causeStr   string
			delta      int64
			receiptAt  time.Time
			insertedAt time.Time
		)
		if err := rows.Scan(&spaceStr, &causeStr, &delta, &receiptAt, &insertedAt); err != nil {
			return store.Page[uploaddiff.DifferenceRecord]{}, fmt.Errorf("scanning upload diff: %w", err)
		}
		spaceDID, err := did.Parse(spaceStr)
		if err != nil {
			return store.Page[uploaddiff.DifferenceRecord]{}, fmt.Errorf("parsing space DID: %w", err)
		}
		cause, err := cid.Parse(causeStr)
		if err != nil {
			return store.Page[uploaddiff.DifferenceRecord]{}, fmt.Errorf("parsing cause CID: %w", err)
		}
		records = append(records, uploaddiff.DifferenceRecord{
			Space:      spaceDID,
			Cause:      cause,
			Delta:      delta,
			ReceiptAt:  receiptAt,
			InsertedAt: insertedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return store.Page[uploaddiff.DifferenceRecord]{}, fmt.Errorf("iterating upload diffs: %w", err)
	}

	var cursor *string
	if len(records) > limit {
		last := records[limit-1]
		c := store.EncodeCursor(last.ReceiptAt, last.Cause.String())
		cursor = &c
		records = records[:limit]
	}
	return store.Page[uploaddiff.DifferenceRecord]{Results: records, Cursor: cursor}, nil
}
