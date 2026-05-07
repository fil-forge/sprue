// Package postgres provides a PostgreSQL-backed implementation of agent.Store.
// Metadata indices live in Postgres; message payloads remain in S3 (matching
// the AWS backend).
package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fil-forge/go-ucanto/core/car"
	"github.com/fil-forge/go-ucanto/core/dag/blockstore"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/message"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/fil-forge/sprue/pkg/store/agent"
	cid "github.com/ipfs/go-cid"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool       *pgxpool.Pool
	s3         *s3.Client
	bucketName string
}

var _ agent.Store = (*Store)(nil)

func New(pool *pgxpool.Pool, s3Client *s3.Client, bucketName string) *Store {
	return &Store{
		pool:       pool,
		s3:         s3Client,
		bucketName: bucketName,
	}
}

func (s *Store) Initialize(ctx context.Context) error {
	if _, err := s.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &s.bucketName}); err != nil {
		if _, err := s.s3.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &s.bucketName}); err != nil {
			return fmt.Errorf("creating S3 bucket %q: %w", s.bucketName, err)
		}
	}
	return nil
}

func (s *Store) Shutdown(ctx context.Context) error {
	return nil
}

func (s *Store) GetInvocation(ctx context.Context, task cid.Cid) (invocation.Invocation, error) {
	root, bs, err := s.getByTask(ctx, task, "in")
	if err != nil {
		return nil, fmt.Errorf("getting invocation for task %s: %w", task, err)
	}
	return invocation.NewInvocationView(cidlink.Link{Cid: root}, bs)
}

func (s *Store) GetReceipt(ctx context.Context, task cid.Cid) (receipt.AnyReceipt, error) {
	root, bs, err := s.getByTask(ctx, task, "out")
	if err != nil {
		return nil, fmt.Errorf("getting receipt for task %s: %w", task, err)
	}
	return receipt.NewAnyReceipt(cidlink.Link{Cid: root}, bs)
}

func (s *Store) getByTask(ctx context.Context, task cid.Cid, kind string) (cid.Cid, blockstore.BlockReader, error) {
	var identifier string
	err := s.pool.QueryRow(ctx, `
		SELECT identifier FROM agent_index WHERE task = $1 AND kind = $2
	`, task.String(), kind).Scan(&identifier)
	if errors.Is(err, pgx.ErrNoRows) {
		if kind == "in" {
			return cid.Undef, nil, agent.ErrInvocationNotFound
		}
		return cid.Undef, nil, agent.ErrReceiptNotFound
	}
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("querying agent_index: %w", err)
	}
	parts := strings.SplitN(identifier, "@", 2)
	if len(parts) != 2 {
		return cid.Undef, nil, fmt.Errorf("invalid identifier format: %s", identifier)
	}
	root, err := cid.Parse(parts[0])
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("parsing root CID: %w", err)
	}
	msgRoot, err := cid.Parse(parts[1])
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("parsing message root CID: %w", err)
	}

	out, err := s.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucketName,
		Key:    aws.String(toMessagePath(msgRoot)),
	})
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("getting message from S3: %w", err)
	}
	defer out.Body.Close()

	_, blocks, err := car.Decode(out.Body)
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("decoding CAR: %w", err)
	}
	bs, err := blockstore.NewBlockStore(blockstore.WithBlocksIterator(blocks))
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("creating blockstore: %w", err)
	}
	return root, bs, nil
}

// Write uploads the agent message payload to S3 and records every index entry
// in a single atomic INSERT. The payload is written before the index so that a
// partial failure leaves (at worst) an orphan S3 object rather than a dangling
// index pointer to a missing payload. All work runs on the caller's context,
// so cancellation propagates through both the S3 and Postgres calls.
func (s *Store) Write(ctx context.Context, msg message.AgentMessage, index []agent.IndexEntry, source []byte) error {
	msgRoot, err := ipldutil.ToCID(msg.Root().Link())
	if err != nil {
		return fmt.Errorf("converting message root link to CID: %w", err)
	}

	if _, err := s.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &s.bucketName,
		Key:    aws.String(toMessagePath(msgRoot)),
		Body:   bytes.NewReader(source),
	}); err != nil {
		return fmt.Errorf("writing agent message to S3: %w", err)
	}

	// agent.Index can yield the same (task, kind) pair more than once (e.g. a
	// receipt's Ran() re-yields its invocation). Dedup by primary key so the
	// batched INSERT below doesn't trip "ON CONFLICT DO UPDATE command cannot
	// affect row a second time". Duplicates within a single message carry the
	// same identifier by construction, so last-wins is safe.
	type indexKey struct{ task, kind string }
	rows := make(map[indexKey]string)
	for _, entry := range index {
		if entry.Invocation != nil {
			invRoot, err := ipldutil.ToCID(entry.Invocation.Invocation.Link())
			if err != nil {
				return fmt.Errorf("converting invocation root link to CID: %w", err)
			}
			rows[indexKey{entry.Invocation.Task.String(), "in"}] = fmt.Sprintf("%s@%s", invRoot, msgRoot)
		}
		if entry.Receipt != nil {
			rcptRoot, err := ipldutil.ToCID(entry.Receipt.Receipt.Root().Link())
			if err != nil {
				return fmt.Errorf("converting receipt root link to CID: %w", err)
			}
			rows[indexKey{entry.Receipt.Task.String(), "out"}] = fmt.Sprintf("%s@%s", rcptRoot, msgRoot)
		}
	}

	if len(rows) == 0 {
		return nil
	}

	placeholders := make([]string, 0, len(rows))
	args := make([]any, 0, 3*len(rows))
	i := 0
	for k, identifier := range rows {
		placeholders = append(placeholders, fmt.Sprintf("($%d, $%d, $%d)", 3*i+1, 3*i+2, 3*i+3))
		args = append(args, k.task, k.kind, identifier)
		i++
	}
	query := "INSERT INTO agent_index (task, kind, identifier) VALUES " +
		strings.Join(placeholders, ", ") +
		" ON CONFLICT (task, kind) DO UPDATE SET identifier = EXCLUDED.identifier"
	if _, err := s.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("writing agent index: %w", err)
	}
	return nil
}

func toMessagePath(msg cid.Cid) string {
	return fmt.Sprintf("%s/%s", msg, msg)
}
