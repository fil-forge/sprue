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
	"github.com/fil-forge/sprue/pkg/store"
	dlgstore "github.com/fil-forge/sprue/pkg/store/delegation"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/delegation"
	"github.com/fil-forge/ucantone/ucan/invocation"
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

func (s *Store) PutMany(ctx context.Context, tokens []ucan.Token, cause cid.Cid) error {
	now := time.Now().UTC()
	for _, token := range tokens {
		link := token.Link().String()

		var body []byte
		var err error
		if dlg, ok := token.(ucan.Delegation); ok {
			body, err = delegation.Encode(dlg)
			if err != nil {
				return fmt.Errorf("encoding delegation %s: %w", link, err)
			}
		} else if inv, ok := token.(ucan.Invocation); ok {
			body, err = invocation.Encode(inv)
			if err != nil {
				return fmt.Errorf("encoding invocation %s: %w", link, err)
			}
		} else {
			return fmt.Errorf("unsupported token type: %T", token)
		}

		if _, err := s.s3.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &s.bucketName,
			Key:    aws.String(link),
			Body:   bytes.NewReader(body),
		}); err != nil {
			return fmt.Errorf("storing delegation %s in S3: %w", link, err)
		}

		var aud did.DID
		// audience may be nil if the token is an invocation
		if token.Audience() != nil {
			aud = token.Audience().DID()
		} else {
			aud = token.Subject().DID()
		}
		var causeStr *string
		if cause != cid.Undef {
			c := cause.String()
			causeStr = &c
		}
		var expiration *int64
		if exp := token.Expiration(); exp != nil {
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
		`, link, aud.String(), token.Issuer().DID().String(), causeStr, expiration, now); err != nil {
			return fmt.Errorf("indexing delegation %s: %w", link, err)
		}
	}
	return nil
}

func (s *Store) ListByAudience(ctx context.Context, audience did.DID, options ...dlgstore.ListByAudienceOption) (store.Page[ucan.Token], error) {
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
		return store.Page[ucan.Token]{}, fmt.Errorf("querying delegations by audience: %w", err)
	}
	defer rows.Close()

	links := make([]string, 0, limit)
	for rows.Next() {
		var link string
		if err := rows.Scan(&link); err != nil {
			return store.Page[ucan.Token]{}, fmt.Errorf("scanning delegation: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return store.Page[ucan.Token]{}, fmt.Errorf("iterating delegations: %w", err)
	}

	var cursor *string
	if len(links) > limit {
		last := links[limit-1]
		cursor = &last
		links = links[:limit]
	}

	results := make([]ucan.Token, 0, len(links))
	for _, link := range links {
		token, err := s.fetchToken(ctx, link)
		if err != nil {
			return store.Page[ucan.Token]{}, fmt.Errorf("fetching token %s: %w", link, err)
		}
		results = append(results, token)
	}

	return store.Page[ucan.Token]{Results: results, Cursor: cursor}, nil
}

// fetchToken retrieves and decodes a delegation/invocation from S3 by its link
// CID string.
func (s *Store) fetchToken(ctx context.Context, link string) (ucan.Token, error) {
	out, err := s.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucketName,
		Key:    aws.String(link),
	})
	if err != nil {
		return nil, fmt.Errorf("getting token %s from S3: %w", link, err)
	}
	defer out.Body.Close()

	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token %s body from S3: %w", link, err)
	}

	inv, err := invocation.Decode(body)
	if err != nil {
		dlg, err := delegation.Decode(body)
		if err != nil {
			return nil, fmt.Errorf("decoding token %s: %w", link, err)
		}
		return dlg, nil
	}
	return inv, nil
}
