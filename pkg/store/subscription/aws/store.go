package aws

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/subscription"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
)

const customerV2IndexName = "customerV2"

var DynamoSubscriptionTableProps = struct {
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
			AttributeName: aws.String("customer"),
			AttributeType: types.ScalarAttributeTypeS,
		},
	},
	GSI: []types.GlobalSecondaryIndex{
		{
			IndexName: aws.String(customerV2IndexName),
			KeySchema: []types.KeySchemaElement{
				{
					AttributeName: aws.String("customer"),
					KeyType:       types.KeyTypeHash,
				},
				{
					AttributeName: aws.String("provider"),
					KeyType:       types.KeyTypeRange,
				},
				{
					AttributeName: aws.String("subscription"),
					KeyType:       types.KeyTypeRange,
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

var _ subscription.Store = (*Store)(nil)

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
		KeySchema:              DynamoSubscriptionTableProps.KeySchema,
		AttributeDefinitions:   DynamoSubscriptionTableProps.Attributes,
		GlobalSecondaryIndexes: DynamoSubscriptionTableProps.GSI,
		BillingMode:            types.BillingModePayPerRequest,
	}); err != nil {
		return fmt.Errorf("creating DynamoDB table %q: %w", s.tableName, err)
	}
	return nil
}

func (s *Store) Add(ctx context.Context, provider did.DID, subscriptionID string, customer did.DID, cause cid.Cid) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.dynamo.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item: map[string]types.AttributeValue{
			"subscription": &types.AttributeValueMemberS{Value: subscriptionID},
			"provider":     &types.AttributeValueMemberS{Value: provider.String()},
			"customer":     &types.AttributeValueMemberS{Value: customer.String()},
			"cause":        &types.AttributeValueMemberS{Value: cause.String()},
			"insertedAt":   &types.AttributeValueMemberS{Value: now},
		},
		ConditionExpression: aws.String("attribute_not_exists(#subscription)"),
		ExpressionAttributeNames: map[string]string{
			"#subscription": "subscription",
		},
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return subscription.ErrSubscriptionExists
		}
		return fmt.Errorf("adding subscription: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, provider did.DID, subscriptionID string) (subscription.Record, error) {
	out, err := s.dynamo.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"subscription": &types.AttributeValueMemberS{Value: subscriptionID},
			"provider":     &types.AttributeValueMemberS{Value: provider.String()},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return subscription.Record{}, fmt.Errorf("getting subscription: %w", err)
	}
	if len(out.Item) == 0 {
		return subscription.Record{}, subscription.ErrSubscriptionNotFound
	}
	return itemToRecord(out.Item)
}

func (s *Store) ListByProviderAndCustomer(ctx context.Context, provider did.DID, customer did.DID, options ...subscription.ListByProviderAndCustomerOption) (store.Page[subscription.Record], error) {
	cfg := subscription.ListByProviderAndCustomerConfig{}
	for _, opt := range options {
		opt(&cfg)
	}

	input := &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		IndexName:              aws.String(customerV2IndexName),
		KeyConditionExpression: aws.String("#customer = :customer AND #provider = :provider"),
		ExpressionAttributeNames: map[string]string{
			"#customer": "customer",
			"#provider": "provider",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":customer": &types.AttributeValueMemberS{Value: customer.String()},
			":provider": &types.AttributeValueMemberS{Value: provider.String()},
		},
	}
	if cfg.Limit != nil {
		input.Limit = aws.Int32(int32(*cfg.Limit))
	}
	if cfg.Cursor != nil {
		// ExclusiveStartKey must include the table primary key (subscription +
		// provider) and the GSI key (customer + provider + subscription).
		input.ExclusiveStartKey = map[string]types.AttributeValue{
			"subscription": &types.AttributeValueMemberS{Value: *cfg.Cursor},
			"provider":     &types.AttributeValueMemberS{Value: provider.String()},
			"customer":     &types.AttributeValueMemberS{Value: customer.String()},
		}
	}

	out, err := s.dynamo.Query(ctx, input)
	if err != nil {
		return store.Page[subscription.Record]{}, fmt.Errorf("listing subscriptions by provider and customer: %w", err)
	}

	records := make([]subscription.Record, 0, len(out.Items))
	for _, item := range out.Items {
		rec, err := itemToRecord(item)
		if err != nil {
			return store.Page[subscription.Record]{}, err
		}
		records = append(records, rec)
	}

	var cursor *string
	if out.LastEvaluatedKey != nil {
		if subAttr, ok := out.LastEvaluatedKey["subscription"].(*types.AttributeValueMemberS); ok {
			cursor = &subAttr.Value
		}
	}

	return store.Page[subscription.Record]{Results: records, Cursor: cursor}, nil
}

func itemToRecord(item map[string]types.AttributeValue) (subscription.Record, error) {
	subscriptionAttr, ok := item["subscription"].(*types.AttributeValueMemberS)
	if !ok {
		return subscription.Record{}, fmt.Errorf("missing or invalid subscription attribute")
	}

	providerAttr, ok := item["provider"].(*types.AttributeValueMemberS)
	if !ok {
		return subscription.Record{}, fmt.Errorf("missing or invalid provider attribute")
	}
	provider, err := did.Parse(providerAttr.Value)
	if err != nil {
		return subscription.Record{}, fmt.Errorf("parsing provider DID: %w", err)
	}

	customerAttr, ok := item["customer"].(*types.AttributeValueMemberS)
	if !ok {
		return subscription.Record{}, fmt.Errorf("missing or invalid customer attribute")
	}
	customer, err := did.Parse(customerAttr.Value)
	if err != nil {
		return subscription.Record{}, fmt.Errorf("parsing customer DID: %w", err)
	}

	var cause cid.Cid
	if causeAttr, ok := item["cause"].(*types.AttributeValueMemberS); ok {
		cause, err = cid.Parse(causeAttr.Value)
		if err != nil {
			return subscription.Record{}, fmt.Errorf("parsing cause CID: %w", err)
		}
	}

	rec := subscription.Record{
		Subscription: subscriptionAttr.Value,
		Provider:     provider,
		Customer:     customer,
		Cause:        cause,
	}
	if v, ok := item["insertedAt"].(*types.AttributeValueMemberS); ok {
		rec.InsertedAt, _ = time.Parse(time.RFC3339Nano, v.Value)
	}
	return rec, nil
}
