// Package postgres provides a PostgreSQL-backed implementation of upload_diff.Store.
package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

func (s *Store) Put(ctx context.Context, provider did.DID, space did.DID, subscription string, cause cid.Cid, delta int64, receiptAt time.Time) error {
	return PutWith(ctx, s.pool, provider, space, subscription, cause, delta, receiptAt)
}

// PutWith inserts an upload diff row using the provided querier, allowing the
// write to participate in an external transaction. It exists so the upload
// store can batch upload-diff writes with its own updates in one atomic unit.
func PutWith(ctx context.Context, q pgxExec, provider did.DID, space did.DID, subscription string, cause cid.Cid, delta int64, receiptAt time.Time) error {
	// ON CONFLICT DO NOTHING is what makes a replayed cause the no-op the schema
	// describes. Without it a repeat of the same (provider, space, receipt_at,
	// cause) raises a unique violation, and because this write shares the
	// caller's transaction that error would abort the whole accounting update —
	// failing a change that has already been applied to the upload table.
	_, err := q.Exec(ctx, `
		INSERT INTO upload_diff (provider, space, receipt_at, cause, subscription, delta)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (provider, space, receipt_at, cause) DO NOTHING
	`, provider.String(), space.String(), receiptAt.UTC(), cause.String(), subscription, delta)
	if err != nil {
		return fmt.Errorf("putting upload diff: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, provider did.DID, space did.DID, after time.Time, options ...uploaddiff.ListOption) (store.Page[uploaddiff.DifferenceRecord], error) {
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
	args = append(args, provider.String(), space.String())
	conds = append(conds, "provider = $1", "space = $2")

	if cfg.Cursor != nil {
		receiptAt, cause, err := decodeCursor(*cfg.Cursor)
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
		SELECT provider, space, subscription, cause, delta, receipt_at, inserted_at
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
			providerStr  string
			spaceStr     string
			subscription string
			causeStr     string
			delta        int64
			receiptAt    time.Time
			insertedAt   time.Time
		)
		if err := rows.Scan(&providerStr, &spaceStr, &subscription, &causeStr, &delta, &receiptAt, &insertedAt); err != nil {
			return store.Page[uploaddiff.DifferenceRecord]{}, fmt.Errorf("scanning upload diff: %w", err)
		}
		providerDID, err := did.Parse(providerStr)
		if err != nil {
			return store.Page[uploaddiff.DifferenceRecord]{}, fmt.Errorf("parsing provider DID: %w", err)
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
			Provider:     providerDID,
			Space:        spaceDID,
			Subscription: subscription,
			Cause:        cause,
			Delta:        delta,
			ReceiptAt:    receiptAt,
			InsertedAt:   insertedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return store.Page[uploaddiff.DifferenceRecord]{}, fmt.Errorf("iterating upload diffs: %w", err)
	}

	var cursor *string
	if len(records) > limit {
		last := records[limit-1]
		c := encodeCursor(last.ReceiptAt, last.Cause.String())
		cursor = &c
		records = records[:limit]
	}
	return store.Page[uploaddiff.DifferenceRecord]{Results: records, Cursor: cursor}, nil
}

type cursorPayload struct {
	ReceiptAt time.Time `json:"r"`
	Cause     string    `json:"c"`
}

func encodeCursor(receiptAt time.Time, cause string) string {
	b, _ := json.Marshal(cursorPayload{ReceiptAt: receiptAt.UTC(), Cause: cause})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(cursor string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	var p cursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return time.Time{}, "", err
	}
	return p.ReceiptAt, p.Cause, nil
}
