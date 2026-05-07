package aws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fil-forge/go-libstoracha/jobqueue"
	"github.com/fil-forge/go-ucanto/core/car"
	"github.com/fil-forge/go-ucanto/core/dag/blockstore"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/message"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/fil-forge/sprue/pkg/store/agent"
	cid "github.com/ipfs/go-cid"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
)

const (
	// writeConcurrency is the number of concurrent writes to AWS allowed by the store
	writeConcurrency = 25
	// writeTimeout is the maximum duration for a write operation to AWS
	writeTimeout = time.Second * 30
)

var DynamoAgentTableProps = struct {
	KeySchema  []types.KeySchemaElement
	Attributes []types.AttributeDefinition
}{
	KeySchema: []types.KeySchemaElement{
		{
			AttributeName: aws.String("taskkind"),
			KeyType:       types.KeyTypeHash,
		},
		{
			AttributeName: aws.String("identifier"),
			KeyType:       types.KeyTypeRange,
		},
	},
	Attributes: []types.AttributeDefinition{
		{
			AttributeName: aws.String("taskkind"),
			AttributeType: types.ScalarAttributeTypeS,
		},
		{
			AttributeName: aws.String("identifier"),
			AttributeType: types.ScalarAttributeTypeS,
		},
	},
}

type awsWriteJob struct {
	s3Put     *s3.PutObjectInput
	dynamoPut *dynamodb.PutItemInput
	callback  func(error)
}

type Store struct {
	writeQueue *jobqueue.JobQueue[awsWriteJob]
	dynamo     *dynamodb.Client
	s3         *s3.Client
	tableName  string
	bucketName string
}

var _ agent.Store = (*Store)(nil)

func New(dynamo *dynamodb.Client, tableName string, s3 *s3.Client, bucketName string) *Store {
	handler := jobqueue.JobHandler(func(ctx context.Context, job awsWriteJob) error {
		var err error
		if job.s3Put != nil {
			_, err = s3.PutObject(ctx, job.s3Put)
		}
		if job.dynamoPut != nil {
			_, err = dynamo.PutItem(ctx, job.dynamoPut)
		}
		job.callback(err)
		return nil
	})
	q := jobqueue.NewJobQueue[awsWriteJob](
		handler,
		jobqueue.WithConcurrency(writeConcurrency),
		jobqueue.WithJobTimeout(writeTimeout),
	)
	q.Startup()
	return &Store{
		writeQueue: q,
		dynamo:     dynamo,
		s3:         s3,
		tableName:  tableName,
		bucketName: bucketName,
	}
}

// Initialize creates the DynamoDB table and S3 bucket if they do not already exist.
func (s *Store) Initialize(ctx context.Context) error {
	var bucketExists bool
	if _, err := s.s3.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: &s.bucketName,
	}); err == nil {
		bucketExists = true
	}
	if !bucketExists {
		_, err := s.s3.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: &s.bucketName,
		})
		if err != nil {
			return fmt.Errorf("creating S3 bucket %q: %w", s.bucketName, err)
		}
	}

	var tableExists bool
	if _, err := s.dynamo.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(s.tableName),
	}); err == nil {
		tableExists = true
	}
	if !tableExists {
		if _, err := s.dynamo.CreateTable(ctx, &dynamodb.CreateTableInput{
			TableName:            aws.String(s.tableName),
			KeySchema:            DynamoAgentTableProps.KeySchema,
			AttributeDefinitions: DynamoAgentTableProps.Attributes,
			BillingMode:          types.BillingModePayPerRequest,
		}); err != nil {
			return fmt.Errorf("creating DynamoDB table %q: %w", s.tableName, err)
		}
	}
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

// getByTask is a helper method that retrieves the invocation or receipt root
// CID and blocks for a given task CID and kind ("in" for invocation or "out" for receipt).
func (s *Store) getByTask(ctx context.Context, task cid.Cid, kind string) (cid.Cid, blockstore.BlockReader, error) {
	taskkind := fmt.Sprintf("%s.%s", task, kind)
	queryInput := &dynamodb.QueryInput{
		TableName:              &s.tableName,
		KeyConditionExpression: aws.String("taskkind = :taskkind"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":taskkind": &types.AttributeValueMemberS{Value: taskkind},
		},
		Limit: aws.Int32(1),
	}
	queryRes, err := s.dynamo.Query(ctx, queryInput)
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("querying DynamoDB for %s: %w", taskkind, err)
	}
	if len(queryRes.Items) != 1 {
		if kind == "in" {
			return cid.Undef, nil, agent.ErrInvocationNotFound
		} else {
			return cid.Undef, nil, agent.ErrReceiptNotFound
		}
	}

	item := queryRes.Items[0]
	v, ok := item["identifier"]
	if !ok {
		return cid.Undef, nil, fmt.Errorf("missing identifier attribute in DynamoDB item: %v", item)
	}
	sv, ok := v.(*types.AttributeValueMemberS)
	if !ok {
		return cid.Undef, nil, fmt.Errorf("unexpected type for identifier attribute in DynamoDB item: %T", v)
	}
	identifier := sv.Value
	parts := strings.SplitN(identifier, "@", 2)
	if len(parts) != 2 {
		return cid.Undef, nil, fmt.Errorf("invalid identifier format in DynamoDB item: %s", identifier)
	}
	root, err := cid.Parse(parts[0])
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("parsing root CID from identifier: %w", err)
	}
	msgRoot, err := cid.Parse(parts[1])
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("parsing message root CID from identifier: %w", err)
	}

	// now get the agent message from S3
	getRes, err := s.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucketName,
		Key:    aws.String(toMessagePath(msgRoot)),
	})
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("getting message from S3: %w", err)
	}
	defer getRes.Body.Close()

	_, blocks, err := car.Decode(getRes.Body)
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("decoding CAR: %w", err)
	}
	bs, err := blockstore.NewBlockStore(blockstore.WithBlocksIterator(blocks))
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("creating blockstore: %w", err)
	}
	return root, bs, nil
}

func (s *Store) Shutdown(ctx context.Context) error {
	return s.writeQueue.Shutdown(ctx)
}

func (s *Store) Write(ctx context.Context, message message.AgentMessage, index []agent.IndexEntry, source []byte) error {
	var wg sync.WaitGroup
	var writeErrMutex sync.Mutex
	var writeErr error

	msgRoot, err := ipldutil.ToCID(message.Root().Link())
	if err != nil {
		return fmt.Errorf("converting message root link to CID: %w", err)
	}

	callback := func(err error) {
		if err != nil {
			writeErrMutex.Lock()
			writeErr = errors.Join(writeErr, err)
			writeErrMutex.Unlock()
		}
		wg.Done()
	}

	wg.Add(1)
	err = s.writeQueue.Queue(ctx, awsWriteJob{
		s3Put: &s3.PutObjectInput{
			Bucket: &s.bucketName,
			Key:    aws.String(toMessagePath(msgRoot)),
			Body:   bytes.NewReader(source),
		},
		callback: callback,
	})
	if err != nil {
		return fmt.Errorf("adding message to write queue: %w", err)
	}

	for _, entry := range index {
		if entry.Invocation != nil {
			invRoot, err := ipldutil.ToCID(entry.Invocation.Invocation.Link())
			if err != nil {
				return fmt.Errorf("converting invocation root link to CID: %w", err)
			}

			wg.Add(1)
			err = s.writeQueue.Queue(ctx, awsWriteJob{
				dynamoPut: &dynamodb.PutItemInput{
					TableName: &s.tableName,
					Item:      toInvocationItem(msgRoot, entry.Invocation.Task, invRoot),
				},
				callback: callback,
			})
			if err != nil {
				return fmt.Errorf("adding invocation index to write queue: %w", err)
			}
		}
		if entry.Receipt != nil {
			rcptRoot, err := ipldutil.ToCID(entry.Receipt.Receipt.Root().Link())
			if err != nil {
				return fmt.Errorf("converting receipt root link to CID: %w", err)
			}

			wg.Add(1)
			err = s.writeQueue.Queue(ctx, awsWriteJob{
				dynamoPut: &dynamodb.PutItemInput{
					TableName: &s.tableName,
					Item:      toReceiptItem(msgRoot, entry.Receipt.Task, rcptRoot),
				},
				callback: callback,
			})
			if err != nil {
				return fmt.Errorf("adding receipt index to write queue: %w", err)
			}
		}
	}

	wg.Wait()
	return writeErr
}

func toMessagePath(message cid.Cid) string {
	return fmt.Sprintf("%s/%s", message, message)
}

func toInvocationItem(message cid.Cid, task cid.Cid, invocation cid.Cid) map[string]types.AttributeValue {
	taskkind, err := attributevalue.Marshal(fmt.Sprintf("%s.in", task))
	if err != nil {
		panic(fmt.Sprintf("marshaling taskkind: %v", err))
	}
	identifier, err := attributevalue.Marshal(fmt.Sprintf("%s@%s", invocation, message))
	if err != nil {
		panic(fmt.Sprintf("marshaling identifier: %v", err))
	}
	return map[string]types.AttributeValue{
		"taskkind":   taskkind,
		"identifier": identifier,
	}
}

func toReceiptItem(message cid.Cid, task cid.Cid, receipt cid.Cid) map[string]types.AttributeValue {
	taskkind, err := attributevalue.Marshal(fmt.Sprintf("%s.out", task))
	if err != nil {
		panic(fmt.Sprintf("marshaling taskkind: %v", err))
	}
	identifier, err := attributevalue.Marshal(fmt.Sprintf("%s@%s", receipt, message))
	if err != nil {
		panic(fmt.Sprintf("marshaling identifier: %v", err))
	}
	return map[string]types.AttributeValue{
		"taskkind":   taskkind,
		"identifier": identifier,
	}
}
