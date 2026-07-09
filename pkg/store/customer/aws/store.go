package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/fil-forge/sprue/pkg/internal/timeutil"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/customer"
	"github.com/fil-forge/ucantone/did"
)

var DynamoCustomerTableProps = struct {
	KeySchema  []types.KeySchemaElement
	Attributes []types.AttributeDefinition
	GSI        []types.GlobalSecondaryIndex
}{
	KeySchema: []types.KeySchemaElement{
		{
			AttributeName: aws.String("customer"),
			KeyType:       types.KeyTypeHash,
		},
	},
	Attributes: []types.AttributeDefinition{
		{
			AttributeName: aws.String("customer"),
			AttributeType: types.ScalarAttributeTypeS,
		},
		{
			AttributeName: aws.String("externalAccount"),
			AttributeType: types.ScalarAttributeTypeS,
		},
	},
	GSI: []types.GlobalSecondaryIndex{
		{
			IndexName: aws.String("externalAccount"),
			KeySchema: []types.KeySchemaElement{
				{
					AttributeName: aws.String("externalAccount"),
					KeyType:       types.KeyTypeHash,
				},
			},
			Projection: &types.Projection{
				ProjectionType:   types.ProjectionTypeInclude,
				NonKeyAttributes: []string{"customer", "product"},
			},
		},
	},
}

type Store struct {
	dynamo    *dynamodb.Client
	tableName string
}

var _ customer.Store = (*Store)(nil)

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
		KeySchema:              DynamoCustomerTableProps.KeySchema,
		AttributeDefinitions:   DynamoCustomerTableProps.Attributes,
		GlobalSecondaryIndexes: DynamoCustomerTableProps.GSI,
		BillingMode:            types.BillingModePayPerRequest,
	}); err != nil {
		return fmt.Errorf("creating DynamoDB table %q: %w", s.tableName, err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, customerID did.DID) (customer.Record, error) {
	out, err := s.dynamo.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"customer": &types.AttributeValueMemberS{Value: customerID.String()},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return customer.Record{}, fmt.Errorf("getting customer: %w", err)
	}
	if len(out.Item) == 0 {
		return customer.Record{}, customer.ErrCustomerNotFound
	}
	return itemToRecord(out.Item)
}

func (s *Store) Add(ctx context.Context, customerID did.DID, externalAccount *string, product did.DID, details map[string]any, reservedCapacity *uint64) error {
	now := time.Now().UTC().Format(timeutil.SimplifiedISO8601)
	item := map[string]types.AttributeValue{
		"customer":   &types.AttributeValueMemberS{Value: customerID.String()},
		"product":    &types.AttributeValueMemberS{Value: product.String()},
		"insertedAt": &types.AttributeValueMemberS{Value: now},
	}
	if externalAccount != nil {
		item["externalAccount"] = &types.AttributeValueMemberS{Value: *externalAccount}
	}
	if reservedCapacity != nil {
		item["reservedCapacity"] = &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", *reservedCapacity)}
	}
	if len(details) > 0 {
		detailsJSON, err := json.Marshal(details)
		if err != nil {
			return fmt.Errorf("marshalling details: %w", err)
		}
		item["details"] = &types.AttributeValueMemberS{Value: string(detailsJSON)}
	}

	_, err := s.dynamo.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(#customer)"),
		ExpressionAttributeNames: map[string]string{
			"#customer": "customer",
		},
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return customer.ErrCustomerExists
		}
		return fmt.Errorf("adding customer: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, options ...customer.ListOption) (store.Page[customer.Record], error) {
	cfg := customer.ListConfig{}
	for _, opt := range options {
		opt(&cfg)
	}

	input := &dynamodb.ScanInput{
		TableName: aws.String(s.tableName),
	}
	if cfg.Limit != nil {
		input.Limit = aws.Int32(int32(*cfg.Limit))
	}
	if cfg.Cursor != nil {
		input.ExclusiveStartKey = map[string]types.AttributeValue{
			"customer": &types.AttributeValueMemberS{Value: *cfg.Cursor},
		}
	}

	out, err := s.dynamo.Scan(ctx, input)
	if err != nil {
		return store.Page[customer.Record]{}, fmt.Errorf("listing customers: %w", err)
	}

	records := make([]customer.Record, 0, len(out.Items))
	for _, item := range out.Items {
		rec, err := itemToRecord(item)
		if err != nil {
			return store.Page[customer.Record]{}, err
		}
		records = append(records, rec)
	}

	var cursor *string
	if out.LastEvaluatedKey != nil {
		if customerAttr, ok := out.LastEvaluatedKey["customer"].(*types.AttributeValueMemberS); ok {
			cursor = &customerAttr.Value
		}
	}

	return store.Page[customer.Record]{Results: records, Cursor: cursor}, nil
}

func (s *Store) UpdateProduct(ctx context.Context, customerID did.DID, product did.DID) error {
	now := time.Now().UTC().Format(timeutil.SimplifiedISO8601)
	_, err := s.dynamo.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"customer": &types.AttributeValueMemberS{Value: customerID.String()},
		},
		UpdateExpression:    aws.String("SET #product = :product, #updatedAt = :updatedAt"),
		ConditionExpression: aws.String("attribute_exists(#customer)"),
		ExpressionAttributeNames: map[string]string{
			"#customer":  "customer",
			"#product":   "product",
			"#updatedAt": "updatedAt",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":product":   &types.AttributeValueMemberS{Value: product.String()},
			":updatedAt": &types.AttributeValueMemberS{Value: now},
		},
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return customer.ErrCustomerNotFound
		}
		return fmt.Errorf("updating customer product: %w", err)
	}
	return nil
}

func itemToRecord(item map[string]types.AttributeValue) (customer.Record, error) {
	customerAttr, ok := item["customer"].(*types.AttributeValueMemberS)
	if !ok {
		return customer.Record{}, fmt.Errorf("missing or invalid customer attribute")
	}
	customerID, err := did.Parse(customerAttr.Value)
	if err != nil {
		return customer.Record{}, fmt.Errorf("parsing customer DID: %w", err)
	}

	productAttr, ok := item["product"].(*types.AttributeValueMemberS)
	if !ok {
		return customer.Record{}, fmt.Errorf("missing or invalid product attribute")
	}
	product, err := did.Parse(productAttr.Value)
	if err != nil {
		return customer.Record{}, fmt.Errorf("parsing product DID: %w", err)
	}

	rec := customer.Record{
		Customer: customerID,
		Product:  product,
	}

	if externalAccAttr, ok := item["externalAccount"].(*types.AttributeValueMemberS); ok {
		rec.ExternalAccount = &externalAccAttr.Value
	}

	if capAttr, ok := item["reservedCapacity"].(*types.AttributeValueMemberN); ok {
		var cap uint64
		if _, err := fmt.Sscanf(capAttr.Value, "%d", &cap); err != nil {
			return customer.Record{}, fmt.Errorf("parsing reservedCapacity: %w", err)
		}
		rec.ReservedCapacity = &cap
	}

	if detailsAttr, ok := item["details"].(*types.AttributeValueMemberS); ok {
		var details map[string]any
		if err := json.Unmarshal([]byte(detailsAttr.Value), &details); err != nil {
			return customer.Record{}, fmt.Errorf("unmarshalling details: %w", err)
		}
		rec.Details = details
	}

	if v, ok := item["insertedAt"].(*types.AttributeValueMemberS); ok {
		rec.InsertedAt, _ = time.Parse(timeutil.SimplifiedISO8601, v.Value)
	}
	if v, ok := item["updatedAt"].(*types.AttributeValueMemberS); ok {
		rec.UpdatedAt, _ = time.Parse(timeutil.SimplifiedISO8601, v.Value)
	}

	return rec, nil
}
