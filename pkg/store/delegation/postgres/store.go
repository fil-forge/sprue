// Package postgres provides a PostgreSQL-backed implementation of delegation.Store.
// Metadata lives in Postgres; the delegation payload archives remain in S3.
package postgres

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/store"
	dlgstore "github.com/fil-forge/sprue/pkg/store/delegation"
	"github.com/ipfs/go-cid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultListLimit = 1000

type Store struct {
	pool       *pgxpool.Pool
	s3         *s3.Client
	bucketName string
}

var _ dlgstore.Store = (*Store)(nil)

func New(pool *pgxpool.Pool, s3Client *s3.Client, bucketName string) *Store {
	return &Store{pool: pool, s3: s3Client, bucketName: bucketName}
}

// Initialize ensures the S3 bucket exists. Table schema is managed by goose.
func (s *Store) Initialize(ctx context.Context) error {
	if _, err := s.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &s.bucketName}); err != nil {
		if _, err := s.s3.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &s.bucketName}); err != nil {
			return fmt.Errorf("creating S3 bucket %q: %w", s.bucketName, err)
		}
	}
	return nil
}

func (s *Store) PutMany(ctx context.Context, delegations []delegation.Delegation, cause cid.Cid) error {
	now := time.Now().UTC()
	for _, dlg := range delegations {
		link := dlg.Root().Link().String()

		body, err := io.ReadAll(dlg.Archive())
		if err != nil {
			return fmt.Errorf("archiving delegation %s: %w", link, err)
		}
		if _, err := s.s3.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &s.bucketName,
			Key:    aws.String(link),
			Body:   bytes.NewReader(body),
		}); err != nil {
			return fmt.Errorf("storing delegation %s in S3: %w", link, err)
		}

		var causeStr *string
		if cause != cid.Undef {
			c := cause.String()
			causeStr = &c
		}
		var expiration *int64
		if exp := dlg.Expiration(); exp != nil {
			e := int64(*exp)
			expiration = &e
		}

		if _, err := s.pool.Exec(ctx, `
			INSERT INTO delegation (link, audience, issuer, cause, expiration, inserted_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $6)
			ON CONFLICT (link) DO UPDATE
			SET audience = EXCLUDED.audience,
			    issuer = EXCLUDED.issuer,
			    cause = EXCLUDED.cause,
			    expiration = EXCLUDED.expiration,
			    updated_at = EXCLUDED.updated_at
		`, link, dlg.Audience().DID().String(), dlg.Issuer().DID().String(), causeStr, expiration, now); err != nil {
			return fmt.Errorf("indexing delegation %s: %w", link, err)
		}
	}
	return nil
}

func (s *Store) ListByAudience(ctx context.Context, audience did.DID, options ...dlgstore.ListByAudienceOption) (store.Page[delegation.Delegation], error) {
	cfg := dlgstore.ListByAudienceConfig{}
	for _, opt := range options {
		opt(&cfg)
	}
	limit := defaultListLimit
	if cfg.Limit != nil && *cfg.Limit > 0 {
		limit = *cfg.Limit
	}

	args := []any{audience.String(), limit + 1}
	query := `
		SELECT link
		FROM delegation
		WHERE audience = $1
	`
	if cfg.Cursor != nil {
		args = append(args, *cfg.Cursor)
		query += ` AND link > $3`
	}
	query += ` ORDER BY link ASC LIMIT $2`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return store.Page[delegation.Delegation]{}, fmt.Errorf("querying delegations by audience: %w", err)
	}
	defer rows.Close()

	links := make([]string, 0, limit)
	for rows.Next() {
		var link string
		if err := rows.Scan(&link); err != nil {
			return store.Page[delegation.Delegation]{}, fmt.Errorf("scanning delegation: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return store.Page[delegation.Delegation]{}, fmt.Errorf("iterating delegations: %w", err)
	}

	var cursor *string
	if len(links) > limit {
		last := links[limit-1]
		cursor = &last
		links = links[:limit]
	}

	results := make([]delegation.Delegation, 0, len(links))
	for _, link := range links {
		dlg, err := s.fetchDelegation(ctx, link)
		if err != nil {
			return store.Page[delegation.Delegation]{}, fmt.Errorf("fetching delegation %s: %w", link, err)
		}
		results = append(results, dlg)
	}

	return store.Page[delegation.Delegation]{Results: results, Cursor: cursor}, nil
}

func (s *Store) fetchDelegation(ctx context.Context, link string) (delegation.Delegation, error) {
	out, err := s.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucketName,
		Key:    aws.String(link),
	})
	if err != nil {
		return nil, fmt.Errorf("getting delegation from S3: %w", err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("reading delegation from S3: %w", err)
	}
	dlg, err := delegation.Extract(data)
	if err != nil {
		return nil, fmt.Errorf("extracting delegation: %w", err)
	}
	return dlg, nil
}
