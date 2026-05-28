package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
)

var DynamoConsumerTableProps = struct {
	KeySchema  []types.KeySchemaElement
	Attributes []types.AttributeDefinition
	GSI        []types.GlobalSecondaryIndex
}{
	KeySchema: []types.KeySchemaElement{
		{
			AttributeName: aws.String("subscription"),
			KeyType:       types.KeyTypeHash,
		},
		{
			AttributeName: aws.String("provider"),
			KeyType:       types.KeyTypeRange,
		},
	},
	Attributes: []types.AttributeDefinition{
		{
			AttributeName: aws.String("subscription"),
			AttributeType: types.ScalarAttributeTypeS,
		},
		{
			AttributeName: aws.String("provider"),
			AttributeType: types.ScalarAttributeTypeS,
		},
		{
			AttributeName: aws.String("consumer"),
			AttributeType: types.ScalarAttributeTypeS,
		},
		{
			AttributeName: aws.String("customer"),
			AttributeType: types.ScalarAttributeTypeS,
		},
	},
	GSI: []types.GlobalSecondaryIndex{
		// Note: index not in w3infra since interface requires all keys projected
		{
			IndexName: aws.String("consumerV3"),
			KeySchema: []types.KeySchemaElement{
				{
					AttributeName: aws.String("consumer"),
					KeyType:       types.KeyTypeHash,
				},
				{
					AttributeName: aws.String("provider"),
					KeyType:       types.KeyTypeRange,
				},
			},
			Projection: &types.Projection{
				ProjectionType: types.ProjectionTypeAll,
			},
		},
		// Note: index not in w3infra since interface requires all keys projected
		{
			IndexName: aws.String("customerV2"),
			KeySchema: []types.KeySchemaElement{
				{
					AttributeName: aws.String("customer"),
					KeyType:       types.KeyTypeHash,
				},
			},
			Projection: &types.Projection{
				ProjectionType: types.ProjectionTypeAll,
			},
		},
	},
}

type Store struct {
	dynamo    *dynamodb.Client
	tableName string
}

var _ consumer.Store = (*Store)(nil)

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
		TableName:              aws.String(s.tableName),
		KeySchema:              DynamoConsumerTableProps.KeySchema,
		AttributeDefinitions:   DynamoConsumerTableProps.Attributes,
		GlobalSecondaryIndexes: DynamoConsumerTableProps.GSI,
		BillingMode:            types.BillingModePayPerRequest,
	}); err != nil {
		return fmt.Errorf("creating DynamoDB table %q: %w", s.tableName, err)
	}
	return nil
}

func (s *Store) Add(ctx context.Context, provider did.DID, space did.DID, customer did.DID, subscription string, cause cid.Cid) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.dynamo.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item: map[string]types.AttributeValue{
			"subscription": &types.AttributeValueMemberS{Value: subscription},
			"provider":     &types.AttributeValueMemberS{Value: provider.String()},
			"consumer":     &types.AttributeValueMemberS{Value: space.String()},
			"customer":     &types.AttributeValueMemberS{Value: customer.String()},
			"cause":        &types.AttributeValueMemberS{Value: cause.String()},
			"insertedAt":   &types.AttributeValueMemberS{Value: now},
		},
		ConditionExpression: aws.String("attribute_not_exists(subscription)"),
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return consumer.ErrConsumerExists
		}
		return fmt.Errorf("adding consumer: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, provider did.DID, space did.DID) (consumer.Record, error) {
	out, err := s.dynamo.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		IndexName:              aws.String("consumerV3"),
		KeyConditionExpression: aws.String("#consumer = :consumer AND #provider = :provider"),
		ExpressionAttributeNames: map[string]string{
			"#consumer": "consumer",
			"#provider": "provider",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":consumer": &types.AttributeValueMemberS{Value: space.String()},
			":provider": &types.AttributeValueMemberS{Value: provider.String()},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return consumer.Record{}, fmt.Errorf("getting consumer: %w", err)
	}
	if len(out.Items) == 0 {
		return consumer.Record{}, consumer.ErrConsumerNotFound
	}
	return itemToRecord(out.Items[0])
}

func (s *Store) GetBySubscription(ctx context.Context, provider did.DID, subscription string) (consumer.Record, error) {
	out, err := s.dynamo.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"subscription": &types.AttributeValueMemberS{Value: subscription},
			"provider":     &types.AttributeValueMemberS{Value: provider.String()},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return consumer.Record{}, fmt.Errorf("getting consumer by subscription: %w", err)
	}
	if len(out.Item) == 0 {
		return consumer.Record{}, consumer.ErrConsumerNotFound
	}
	return itemToRecord(out.Item)
}

func (s *Store) List(ctx context.Context, space did.DID, options ...consumer.ListOption) (store.Page[consumer.Record], error) {
	cfg := consumer.ListConfig{}
	for _, opt := range options {
		opt(&cfg)
	}

	input := &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		IndexName:              aws.String("consumerV3"),
		KeyConditionExpression: aws.String("#consumer = :consumer"),
		ExpressionAttributeNames: map[string]string{
			"#consumer": "consumer",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":consumer": &types.AttributeValueMemberS{Value: space.String()},
		},
	}
	if cfg.Limit != nil {
		input.Limit = aws.Int32(int32(*cfg.Limit))
	}
	if cfg.Cursor != nil {
		provider, subscription, err := splitCursor(*cfg.Cursor)
		if err != nil {
			return store.Page[consumer.Record]{}, fmt.Errorf("invalid cursor: %w", err)
		}
		input.ExclusiveStartKey = map[string]types.AttributeValue{
			"consumer":     &types.AttributeValueMemberS{Value: space.String()},
			"provider":     &types.AttributeValueMemberS{Value: provider},
			"subscription": &types.AttributeValueMemberS{Value: subscription},
		}
	}

	out, err := s.dynamo.Query(ctx, input)
	if err != nil {
		return store.Page[consumer.Record]{}, fmt.Errorf("listing consumers: %w", err)
	}

	records, err := itemsToRecords(out.Items)
	if err != nil {
		return store.Page[consumer.Record]{}, err
	}

	var cursor *string
	if out.LastEvaluatedKey != nil {
		providerAttr, ok1 := out.LastEvaluatedKey["provider"].(*types.AttributeValueMemberS)
		subAttr, ok2 := out.LastEvaluatedKey["subscription"].(*types.AttributeValueMemberS)
		if ok1 && ok2 {
			c := joinCursor(providerAttr.Value, subAttr.Value)
			cursor = &c
		}
	}

	return store.Page[consumer.Record]{Results: records, Cursor: cursor}, nil
}

func (s *Store) ListByCustomer(ctx context.Context, customer did.DID, options ...consumer.ListByCustomerOption) (store.Page[consumer.Record], error) {
	cfg := consumer.ListByCustomerConfig{}
	for _, opt := range options {
		opt(&cfg)
	}

	input := &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		IndexName:              aws.String("customerV2"),
		KeyConditionExpression: aws.String("#customer = :customer"),
		ExpressionAttributeNames: map[string]string{
			"#customer": "customer",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":customer": &types.AttributeValueMemberS{Value: customer.String()},
		},
	}
	if cfg.Limit != nil {
		input.Limit = aws.Int32(int32(*cfg.Limit))
	}
	if cfg.Cursor != nil {
		provider, subscription, err := splitCursor(*cfg.Cursor)
		if err != nil {
			return store.Page[consumer.Record]{}, fmt.Errorf("invalid cursor: %w", err)
		}
		input.ExclusiveStartKey = map[string]types.AttributeValue{
			"customer":     &types.AttributeValueMemberS{Value: customer.String()},
			"subscription": &types.AttributeValueMemberS{Value: subscription},
			"provider":     &types.AttributeValueMemberS{Value: provider},
		}
	}

	out, err := s.dynamo.Query(ctx, input)
	if err != nil {
		return store.Page[consumer.Record]{}, fmt.Errorf("listing consumers by customer: %w", err)
	}

	records, err := itemsToRecords(out.Items)
	if err != nil {
		return store.Page[consumer.Record]{}, err
	}

	var cursor *string
	if out.LastEvaluatedKey != nil {
		providerAttr, ok1 := out.LastEvaluatedKey["provider"].(*types.AttributeValueMemberS)
		subAttr, ok2 := out.LastEvaluatedKey["subscription"].(*types.AttributeValueMemberS)
		if ok1 && ok2 {
			c := joinCursor(providerAttr.Value, subAttr.Value)
			cursor = &c
		}
	}

	return store.Page[consumer.Record]{Results: records, Cursor: cursor}, nil
}

// joinCursor encodes provider and subscription into an opaque cursor string.
func joinCursor(provider, subscription string) string {
	return provider + "|" + subscription
}

// splitCursor decodes a cursor string back into provider and subscription.
func splitCursor(cursor string) (provider, subscription string, err error) {
	parts := strings.SplitN(cursor, "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("malformed cursor %q", cursor)
	}
	return parts[0], parts[1], nil
}

func itemsToRecords(items []map[string]types.AttributeValue) ([]consumer.Record, error) {
	records := make([]consumer.Record, 0, len(items))
	for _, item := range items {
		rec, err := itemToRecord(item)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}

func itemToRecord(item map[string]types.AttributeValue) (consumer.Record, error) {
	subscriptionAttr, ok := item["subscription"].(*types.AttributeValueMemberS)
	if !ok {
		return consumer.Record{}, fmt.Errorf("missing or invalid subscription attribute")
	}

	providerAttr, ok := item["provider"].(*types.AttributeValueMemberS)
	if !ok {
		return consumer.Record{}, fmt.Errorf("missing or invalid provider attribute")
	}
	provider, err := did.Parse(providerAttr.Value)
	if err != nil {
		return consumer.Record{}, fmt.Errorf("parsing provider DID: %w", err)
	}

	consumerAttr, ok := item["consumer"].(*types.AttributeValueMemberS)
	if !ok {
		return consumer.Record{}, fmt.Errorf("missing or invalid consumer attribute")
	}
	space, err := did.Parse(consumerAttr.Value)
	if err != nil {
		return consumer.Record{}, fmt.Errorf("parsing consumer DID: %w", err)
	}

	customerAttr, ok := item["customer"].(*types.AttributeValueMemberS)
	if !ok {
		return consumer.Record{}, fmt.Errorf("missing or invalid customer attribute")
	}
	customerDID, err := did.Parse(customerAttr.Value)
	if err != nil {
		return consumer.Record{}, fmt.Errorf("parsing customer DID: %w", err)
	}

	var cause cid.Cid
	if causeAttr, ok := item["cause"].(*types.AttributeValueMemberS); ok {
		cause, err = cid.Parse(causeAttr.Value)
		if err != nil {
			return consumer.Record{}, fmt.Errorf("parsing cause CID: %w", err)
		}
	}

	return consumer.Record{
		Subscription: subscriptionAttr.Value,
		Provider:     provider,
		Consumer:     space,
		Customer:     customerDID,
		Cause:        cause,
	}, nil
}
