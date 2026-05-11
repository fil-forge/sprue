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
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/fil-forge/ucantone/ipld/codec/dagcbor"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/ipfs/go-cid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multiformats/go-multihash"
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

func (s *Store) GetInvocation(ctx context.Context, task cid.Cid) (ucan.Invocation, error) {
	_, ct, err := s.getByTask(ctx, task, "in")
	if err != nil {
		return nil, fmt.Errorf("getting invocation for task %s: %w", task, err)
	}
	for _, inv := range ct.Invocations() {
		if inv.Task().Link() == task {
			return inv, nil
		}
	}
	return nil, agent.ErrInvocationNotFound
}

func (s *Store) GetReceipt(ctx context.Context, task cid.Cid) (ucan.Receipt, error) {
	_, ct, err := s.getByTask(ctx, task, "out")
	if err != nil {
		return nil, fmt.Errorf("getting receipt for task %s: %w", task, err)
	}
	rcpt, ok := ct.Receipt(task)
	if !ok {
		return nil, agent.ErrReceiptNotFound
	}
	return rcpt, nil
}

func (s *Store) getByTask(ctx context.Context, task cid.Cid, kind string) (cid.Cid, *container.Container, error) {
	var tokenRootStr, msgRootStr string
	err := s.pool.QueryRow(ctx, `
		SELECT token, message FROM agent_index WHERE task = $1 AND kind = $2
	`, task.String(), kind).Scan(&tokenRootStr, &msgRootStr)
	if errors.Is(err, pgx.ErrNoRows) {
		if kind == "in" {
			return cid.Undef, nil, agent.ErrInvocationNotFound
		}
		return cid.Undef, nil, agent.ErrReceiptNotFound
	}
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("querying agent_index: %w", err)
	}
	tokenRoot, err := cid.Parse(tokenRootStr)
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("parsing root CID: %w", err)
	}
	msgRoot, err := cid.Parse(msgRootStr)
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

	var msg container.Container
	if err := msg.UnmarshalCBOR(out.Body); err != nil {
		return cid.Undef, nil, fmt.Errorf("decoding container: %w", err)
	}

	return tokenRoot, &msg, nil
}

// Write uploads the agent message payload to S3 and records every index entry
// in a single atomic INSERT. The payload is written before the index so that a
// partial failure leaves (at worst) an orphan S3 object rather than a dangling
// index pointer to a missing payload. All work runs on the caller's context,
// so cancellation propagates through both the S3 and Postgres calls.
func (s *Store) Write(ctx context.Context, message ucan.Container, index []agent.IndexEntry) error {
	if len(index) == 0 {
		return nil
	}

	c, ok := message.(*container.Container)
	if !ok {
		c = container.New(
			container.WithInvocations(message.Invocations()...),
			container.WithReceipts(message.Receipts()...),
			container.WithDelegations(message.Delegations()...),
		)
	}

	var buf bytes.Buffer
	if err := c.MarshalCBOR(&buf); err != nil {
		return fmt.Errorf("marshaling agent message to CBOR: %w", err)
	}

	msgRoot, err := cid.V1Builder{Codec: dagcbor.Code, MhType: multihash.SHA2_256}.Sum(buf.Bytes())
	if err != nil {
		return fmt.Errorf("hashing agent message: %w", err)
	}

	if _, err := s.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &s.bucketName,
		Key:    aws.String(toMessagePath(msgRoot)),
		Body:   bytes.NewReader(buf.Bytes()),
	}); err != nil {
		return fmt.Errorf("writing agent message to S3: %w", err)
	}

	// agent.Index can yield the same (task, kind) pair more than once (e.g. a
	// receipt's Ran() re-yields its invocation). Dedup by primary key so the
	// batched INSERT below doesn't trip "ON CONFLICT DO UPDATE command cannot
	// affect row a second time". Duplicates within a single message carry the
	// same identifier by construction, so last-wins is safe.
	type indexKey struct {
		task cid.Cid
		kind string
	}
	rows := make(map[indexKey]agent.IndexEntry)
	for _, entry := range index {
		if entry.Invocation != nil {
			rows[indexKey{entry.Invocation.Task, "in"}] = entry
		}
		if entry.Receipt != nil {
			rows[indexKey{entry.Receipt.Task, "out"}] = entry
		}
	}

	placeholders := make([]string, 0, len(rows))
	args := make([]any, 0, 4*len(rows))
	i := 0
	for k, entry := range rows {
		placeholders = append(placeholders, fmt.Sprintf("($%d, $%d, $%d, $%d)", 4*i+1, 4*i+2, 4*i+3, 4*i+4))
		var token cid.Cid
		if k.kind == "in" {
			token = entry.Invocation.Invocation.Link()
		} else {
			token = entry.Receipt.Receipt.Link()
		}
		args = append(args, k.task, k.kind, token, msgRoot)
		i++
	}
	query := "INSERT INTO agent_index (task, kind, token, message) VALUES " +
		strings.Join(placeholders, ", ") +
		" ON CONFLICT (task, kind) DO UPDATE SET token = EXCLUDED.token, message = EXCLUDED.message"
	if _, err := s.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("writing agent index: %w", err)
	}
	return nil
}

func toMessagePath(msg cid.Cid) string {
	return fmt.Sprintf("%s/%s", msg, msg)
}
