package aws

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/store/metrics"
)

var DynamoMetricsTableProps = struct {
	KeySchema  []types.KeySchemaElement
	Attributes []types.AttributeDefinition
}{
	KeySchema: []types.KeySchemaElement{
		{
			AttributeName: aws.String("name"),
			KeyType:       types.KeyTypeHash,
		},
	},
	Attributes: []types.AttributeDefinition{
		{
			AttributeName: aws.String("name"),
			AttributeType: types.ScalarAttributeTypeS,
		},
	},
}

var DynamoSpaceMetricsTableProps = struct {
	KeySchema  []types.KeySchemaElement
	Attributes []types.AttributeDefinition
}{
	KeySchema: []types.KeySchemaElement{
		{
			AttributeName: aws.String("space"),
			KeyType:       types.KeyTypeHash,
		},
		{
			AttributeName: aws.String("name"),
			KeyType:       types.KeyTypeRange,
		},
	},
	Attributes: []types.AttributeDefinition{
		{
			AttributeName: aws.String("space"),
			AttributeType: types.ScalarAttributeTypeS,
		},
		{
			AttributeName: aws.String("name"),
			AttributeType: types.ScalarAttributeTypeS,
		},
	},
}

// Store is an AWS DynamoDB-backed implementation of metrics.Store. Each metric
// is stored as a row keyed by the metric name, with a numeric total attribute.
type Store struct {
	dynamo    *dynamodb.Client
	tableName string
}

var _ metrics.Store = (*Store)(nil)

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
		KeySchema:            DynamoMetricsTableProps.KeySchema,
		AttributeDefinitions: DynamoMetricsTableProps.Attributes,
		BillingMode:          types.BillingModePayPerRequest,
	}); err != nil {
		return fmt.Errorf("creating DynamoDB table %q: %w", s.tableName, err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context) (map[string]uint64, error) {
	result := map[string]uint64{}
	var lastKey map[string]types.AttributeValue
	for {
		out, err := s.dynamo.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(s.tableName),
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			return nil, fmt.Errorf("scanning metrics: %w", err)
		}
		for _, item := range out.Items {
			nameAttr, ok := item["name"].(*types.AttributeValueMemberS)
			if !ok {
				continue
			}
			totalAttr, ok := item["value"].(*types.AttributeValueMemberN)
			if !ok {
				continue
			}
			total, err := strconv.ParseUint(totalAttr.Value, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parsing total for metric %q: %w", nameAttr.Value, err)
			}
			result[nameAttr.Value] = total
		}
		lastKey = out.LastEvaluatedKey
		if lastKey == nil {
			break
		}
	}
	return result, nil
}

// TransactIncrementTotals returns a list of DynamoDB TransactWriteItems that
// can be used to increment the given metrics by the given amounts in a
// transaction.
func (s *Store) TransactIncrementTotals(inc map[string]uint64) []types.TransactWriteItem {
	items := make([]types.TransactWriteItem, 0, len(inc))
	for metric, delta := range inc {
		items = append(items, types.TransactWriteItem{
			Update: &types.Update{
				TableName: aws.String(s.tableName),
				Key: map[string]types.AttributeValue{
					"name": &types.AttributeValueMemberS{Value: metric},
				},
				UpdateExpression: aws.String("ADD #value :delta"),
				ExpressionAttributeNames: map[string]string{
					"#value": "value",
				},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":delta": &types.AttributeValueMemberN{Value: strconv.FormatUint(delta, 10)},
				},
			},
		})
	}
	return items
}

func (s *Store) IncrementTotals(ctx context.Context, inc map[string]uint64) error {
	if len(inc) == 0 {
		return nil
	}
	items := s.TransactIncrementTotals(inc)
	if _, err := s.dynamo.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: items,
	}); err != nil {
		return fmt.Errorf("incrementing metrics: %w", err)
	}
	return nil
}

// SpaceStore is an AWS DynamoDB-backed implementation of metrics.SpaceStore.
// Each metric for a space is stored as a row keyed by (space, metric), with a
// numeric total attribute.
type SpaceStore struct {
	dynamo    *dynamodb.Client
	tableName string
}

var _ metrics.SpaceStore = (*SpaceStore)(nil)

func NewSpaceStore(dynamo *dynamodb.Client, tableName string) *SpaceStore {
	return &SpaceStore{dynamo: dynamo, tableName: tableName}
}

// Initialize creates the DynamoDB table if it does not already exist.
func (s *SpaceStore) Initialize(ctx context.Context) error {
	if _, err := s.dynamo.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(s.tableName),
	}); err == nil {
		return nil
	}
	if _, err := s.dynamo.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String(s.tableName),
		KeySchema:            DynamoSpaceMetricsTableProps.KeySchema,
		AttributeDefinitions: DynamoSpaceMetricsTableProps.Attributes,
		BillingMode:          types.BillingModePayPerRequest,
	}); err != nil {
		return fmt.Errorf("creating DynamoDB table %q: %w", s.tableName, err)
	}
	return nil
}

func (s *SpaceStore) Get(ctx context.Context, space did.DID) (map[string]uint64, error) {
	result := map[string]uint64{}
	var lastKey map[string]types.AttributeValue
	for {
		out, err := s.dynamo.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(s.tableName),
			KeyConditionExpression: aws.String("#space = :space"),
			ExpressionAttributeNames: map[string]string{
				"#space": "space",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":space": &types.AttributeValueMemberS{Value: space.String()},
			},
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			return nil, fmt.Errorf("querying space metrics: %w", err)
		}
		for _, item := range out.Items {
			nameAttr, ok := item["name"].(*types.AttributeValueMemberS)
			if !ok {
				continue
			}
			totalAttr, ok := item["value"].(*types.AttributeValueMemberN)
			if !ok {
				continue
			}
			total, err := strconv.ParseUint(totalAttr.Value, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parsing total for metric %q: %w", nameAttr.Value, err)
			}
			result[nameAttr.Value] = total
		}
		lastKey = out.LastEvaluatedKey
		if lastKey == nil {
			break
		}
	}
	return result, nil
}

func (s *SpaceStore) TransactIncrementTotals(space did.DID, inc map[string]uint64) []types.TransactWriteItem {
	items := make([]types.TransactWriteItem, 0, len(inc))
	for metric, delta := range inc {
		items = append(items, types.TransactWriteItem{
			Update: &types.Update{
				TableName: aws.String(s.tableName),
				Key: map[string]types.AttributeValue{
					"space": &types.AttributeValueMemberS{Value: space.String()},
					"name":  &types.AttributeValueMemberS{Value: metric},
				},
				UpdateExpression: aws.String("ADD #value :delta"),
				ExpressionAttributeNames: map[string]string{
					"#value": "value",
				},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":delta": &types.AttributeValueMemberN{Value: strconv.FormatUint(delta, 10)},
				},
			},
		})
	}
	return items
}

func (s *SpaceStore) IncrementTotals(ctx context.Context, space did.DID, inc map[string]uint64) error {
	if len(inc) == 0 {
		return nil
	}
	items := s.TransactIncrementTotals(space, inc)
	if _, err := s.dynamo.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: items,
	}); err != nil {
		return fmt.Errorf("incrementing space metrics: %w", err)
	}
	return nil
}
