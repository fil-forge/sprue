package aws

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/fil-forge/libforge/digestutil"
	"github.com/fil-forge/sprue/pkg/internal/timeutil"
	"github.com/fil-forge/sprue/pkg/store/replica"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
)

var DynamoReplicaTableProps = struct {
	KeySchema  []types.KeySchemaElement
	Attributes []types.AttributeDefinition
}{
	KeySchema: []types.KeySchemaElement{
		{
			AttributeName: aws.String("pk"),
			KeyType:       types.KeyTypeHash,
		},
		{
			AttributeName: aws.String("provider"),
			KeyType:       types.KeyTypeRange,
		},
	},
	Attributes: []types.AttributeDefinition{
		{
			AttributeName: aws.String("pk"),
			AttributeType: types.ScalarAttributeTypeS,
		},
		{
			AttributeName: aws.String("provider"),
			AttributeType: types.ScalarAttributeTypeS,
		},
	},
}

type Store struct {
	dynamo    *dynamodb.Client
	tableName string
}

var _ replica.Store = (*Store)(nil)

func New(dynamo *dynamodb.Client, tableName string) *Store {
	return &Store{dynamo: dynamo, tableName: tableName}
}

// Initialize creates the DynamoDB table if it does not already exist.
func (s *Store) Initialize(ctx context.Context) error {
	if _, err := s.dynamo.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(s.tableName),
	}); err == nil {
		return nil
	}
	if _, err := s.dynamo.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String(s.tableName),
		KeySchema:            DynamoReplicaTableProps.KeySchema,
		AttributeDefinitions: DynamoReplicaTableProps.Attributes,
		BillingMode:          types.BillingModePayPerRequest,
	}); err != nil {
		return fmt.Errorf("creating DynamoDB table %q: %w", s.tableName, err)
	}
	return nil
}

func (s *Store) Add(ctx context.Context, space did.DID, digest multihash.Multihash, provider did.DID, status replica.ReplicationStatus, cause cid.Cid) error {
	now := time.Now().UTC().Format(timeutil.SimplifiedISO8601)
	_, err := s.dynamo.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item: map[string]types.AttributeValue{
			"pk":         &types.AttributeValueMemberS{Value: partitionKey(space, digest)},
			"provider":   &types.AttributeValueMemberS{Value: provider.String()},
			"space":      &types.AttributeValueMemberS{Value: space.String()},
			"digest":     &types.AttributeValueMemberS{Value: digestutil.Format(digest)},
			"status":     &types.AttributeValueMemberS{Value: status.String()},
			"cause":      &types.AttributeValueMemberS{Value: cause.String()},
			"insertedAt": &types.AttributeValueMemberS{Value: now},
			"updatedAt":  &types.AttributeValueMemberS{Value: now},
		},
		ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(provider)"),
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return replica.ErrReplicaExists
		}
		return fmt.Errorf("adding replica: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, space did.DID, digest multihash.Multihash) ([]replica.Record, error) {
	out, err := s.dynamo.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("#pk = :pk"),
		ExpressionAttributeNames: map[string]string{
			"#pk": "pk",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: partitionKey(space, digest)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("listing replicas: %w", err)
	}

	records := make([]replica.Record, 0, len(out.Items))
	for _, item := range out.Items {
		rec, err := itemToRecord(item)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}

func (s *Store) Retry(ctx context.Context, space did.DID, digest multihash.Multihash, provider did.DID, status replica.ReplicationStatus, cause cid.Cid) error {
	now := time.Now().UTC().Format(timeutil.SimplifiedISO8601)
	_, err := s.dynamo.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"pk":       &types.AttributeValueMemberS{Value: partitionKey(space, digest)},
			"provider": &types.AttributeValueMemberS{Value: provider.String()},
		},
		UpdateExpression: aws.String("SET #status = :status, #cause = :cause, #updatedAt = :updatedAt"),
		ExpressionAttributeNames: map[string]string{
			"#status":    "status",
			"#cause":     "cause",
			"#updatedAt": "updatedAt",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status":    &types.AttributeValueMemberS{Value: status.String()},
			":cause":     &types.AttributeValueMemberS{Value: cause.String()},
			":updatedAt": &types.AttributeValueMemberS{Value: now},
		},
		ConditionExpression: aws.String("attribute_exists(pk) AND attribute_exists(provider)"),
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return replica.ErrReplicaNotFound
		}
		return fmt.Errorf("retrying replica: %w", err)
	}
	return nil
}

func (s *Store) SetStatus(ctx context.Context, space did.DID, digest multihash.Multihash, provider did.DID, status replica.ReplicationStatus) error {
	now := time.Now().UTC().Format(timeutil.SimplifiedISO8601)
	_, err := s.dynamo.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"pk":       &types.AttributeValueMemberS{Value: partitionKey(space, digest)},
			"provider": &types.AttributeValueMemberS{Value: provider.String()},
		},
		UpdateExpression: aws.String("SET #status = :status, #updatedAt = :updatedAt"),
		ExpressionAttributeNames: map[string]string{
			"#status":    "status",
			"#updatedAt": "updatedAt",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status":    &types.AttributeValueMemberS{Value: status.String()},
			":updatedAt": &types.AttributeValueMemberS{Value: now},
		},
		ConditionExpression: aws.String("attribute_exists(pk) AND attribute_exists(provider)"),
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return replica.ErrReplicaNotFound
		}
		return fmt.Errorf("setting replica status: %w", err)
	}
	return nil
}

// partitionKey computes the composite hash key: "<space>#<digest>".
func partitionKey(space did.DID, digest multihash.Multihash) string {
	return space.String() + "#" + digestutil.Format(digest)
}

func itemToRecord(item map[string]types.AttributeValue) (replica.Record, error) {
	spaceAttr, ok := item["space"].(*types.AttributeValueMemberS)
	if !ok {
		return replica.Record{}, fmt.Errorf("missing or invalid space attribute")
	}
	space, err := did.Parse(spaceAttr.Value)
	if err != nil {
		return replica.Record{}, fmt.Errorf("parsing space DID: %w", err)
	}

	digestAttr, ok := item["digest"].(*types.AttributeValueMemberS)
	if !ok {
		return replica.Record{}, fmt.Errorf("missing or invalid digest attribute")
	}
	digest, err := digestutil.Parse(digestAttr.Value)
	if err != nil {
		return replica.Record{}, fmt.Errorf("parsing digest: %w", err)
	}

	providerAttr, ok := item["provider"].(*types.AttributeValueMemberS)
	if !ok {
		return replica.Record{}, fmt.Errorf("missing or invalid provider attribute")
	}
	provider, err := did.Parse(providerAttr.Value)
	if err != nil {
		return replica.Record{}, fmt.Errorf("parsing provider DID: %w", err)
	}

	statusAttr, ok := item["status"].(*types.AttributeValueMemberS)
	if !ok {
		return replica.Record{}, fmt.Errorf("missing or invalid status attribute")
	}
	status, err := parseStatus(statusAttr.Value)
	if err != nil {
		return replica.Record{}, err
	}

	causeAttr, ok := item["cause"].(*types.AttributeValueMemberS)
	if !ok {
		return replica.Record{}, fmt.Errorf("missing or invalid cause attribute")
	}
	cause, err := cid.Parse(causeAttr.Value)
	if err != nil {
		return replica.Record{}, fmt.Errorf("parsing cause CID: %w", err)
	}

	rec := replica.Record{
		Space:    space,
		Digest:   digest,
		Provider: provider,
		Status:   status,
		Cause:    cause,
	}

	if createdAtAttr, ok := item["insertedAt"].(*types.AttributeValueMemberS); ok {
		rec.CreatedAt, _ = time.Parse(timeutil.SimplifiedISO8601, createdAtAttr.Value)
	}
	if updatedAtAttr, ok := item["updatedAt"].(*types.AttributeValueMemberS); ok {
		rec.UpdatedAt, _ = time.Parse(timeutil.SimplifiedISO8601, updatedAtAttr.Value)
	}

	return rec, nil
}

func parseStatus(s string) (replica.ReplicationStatus, error) {
	switch s {
	case replica.Allocated.String():
		return replica.Allocated, nil
	case replica.Transferred.String():
		return replica.Transferred, nil
	case replica.Failed.String():
		return replica.Failed, nil
	}
	return 0, fmt.Errorf("unknown replication status %q", s)
}
