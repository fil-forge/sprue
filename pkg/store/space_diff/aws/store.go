package aws

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/internal/timeutil"
	"github.com/fil-forge/sprue/pkg/store"
	spacediff "github.com/fil-forge/sprue/pkg/store/space_diff"
	"github.com/ipfs/go-cid"
)

var DynamoSpaceDiffTableProps = struct {
	KeySchema  []types.KeySchemaElement
	Attributes []types.AttributeDefinition
}{
	KeySchema: []types.KeySchemaElement{
		{
			AttributeName: aws.String("pk"),
			KeyType:       types.KeyTypeHash,
		},
		{
			AttributeName: aws.String("sk"),
			KeyType:       types.KeyTypeRange,
		},
	},
	Attributes: []types.AttributeDefinition{
		{
			AttributeName: aws.String("pk"),
			AttributeType: types.ScalarAttributeTypeS,
		},
		{
			AttributeName: aws.String("sk"),
			AttributeType: types.ScalarAttributeTypeS,
		},
	},
}

type Store struct {
	dynamo    *dynamodb.Client
	tableName string
}

var _ spacediff.Store = (*Store)(nil)

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
		KeySchema:            DynamoSpaceDiffTableProps.KeySchema,
		AttributeDefinitions: DynamoSpaceDiffTableProps.Attributes,
		BillingMode:          types.BillingModePayPerRequest,
	}); err != nil {
		return fmt.Errorf("creating DynamoDB table %q: %w", s.tableName, err)
	}
	return nil
}

func (s *Store) TransactPut(ctx context.Context, provider did.DID, space did.DID, subscription string, cause cid.Cid, delta int64, receiptAt time.Time) types.TransactWriteItem {
	pk := compositeKey(provider.String(), space.String())
	sk := compositeKey(receiptAt.UTC().Format(timeutil.SimplifiedISO8601), cause.String())
	now := time.Now().UTC().Format(timeutil.SimplifiedISO8601)
	return types.TransactWriteItem{
		Put: &types.Put{
			TableName: aws.String(s.tableName),
			Item: map[string]types.AttributeValue{
				"pk":           &types.AttributeValueMemberS{Value: pk},
				"sk":           &types.AttributeValueMemberS{Value: sk},
				"provider":     &types.AttributeValueMemberS{Value: provider.String()},
				"space":        &types.AttributeValueMemberS{Value: space.String()},
				"subscription": &types.AttributeValueMemberS{Value: subscription},
				"cause":        &types.AttributeValueMemberS{Value: cause.String()},
				"delta":        &types.AttributeValueMemberN{Value: strconv.FormatInt(delta, 10)},
				"receiptAt":    &types.AttributeValueMemberS{Value: receiptAt.UTC().Format(timeutil.SimplifiedISO8601)},
				"insertedAt":   &types.AttributeValueMemberS{Value: now},
			},
		},
	}
}

func (s *Store) Put(ctx context.Context, provider did.DID, space did.DID, subscription string, cause cid.Cid, delta int64, receiptAt time.Time) error {
	pk := compositeKey(provider.String(), space.String())
	sk := compositeKey(receiptAt.UTC().Format(timeutil.SimplifiedISO8601), cause.String())
	now := time.Now().UTC().Format(timeutil.SimplifiedISO8601)

	if _, err := s.dynamo.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item: map[string]types.AttributeValue{
			"pk":           &types.AttributeValueMemberS{Value: pk},
			"sk":           &types.AttributeValueMemberS{Value: sk},
			"provider":     &types.AttributeValueMemberS{Value: provider.String()},
			"space":        &types.AttributeValueMemberS{Value: space.String()},
			"subscription": &types.AttributeValueMemberS{Value: subscription},
			"cause":        &types.AttributeValueMemberS{Value: cause.String()},
			"delta":        &types.AttributeValueMemberN{Value: strconv.FormatInt(delta, 10)},
			"receiptAt":    &types.AttributeValueMemberS{Value: receiptAt.UTC().Format(timeutil.SimplifiedISO8601)},
			"insertedAt":   &types.AttributeValueMemberS{Value: now},
		},
	}); err != nil {
		return fmt.Errorf("putting space diff: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, provider did.DID, space did.DID, after time.Time, options ...spacediff.ListOption) (store.Page[spacediff.DifferenceRecord], error) {
	cfg := spacediff.ListConfig{}
	for _, opt := range options {
		opt(&cfg)
	}

	pk := compositeKey(provider.String(), space.String())

	input := &dynamodb.QueryInput{
		TableName: aws.String(s.tableName),
		ExpressionAttributeNames: map[string]string{
			"#pk": "pk",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: pk},
		},
		ScanIndexForward: aws.Bool(true),
	}

	if cfg.Limit != nil {
		input.Limit = aws.Int32(int32(*cfg.Limit))
	}

	if cfg.Cursor != nil {
		// The cursor is the sk of the last item from the previous page. Use it
		// as ExclusiveStartKey for native DynamoDB pagination.
		input.KeyConditionExpression = aws.String("#pk = :pk")
		input.ExclusiveStartKey = map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
			"sk": &types.AttributeValueMemberS{Value: *cfg.Cursor},
		}
	} else if !after.IsZero() {
		// sk format is "<receiptAt>#<cause>". Appending "$" (ASCII 36, just above
		// "#" which is ASCII 35) as the boundary means records where receiptAt
		// equals after sort below the bound and are excluded, matching the
		// strictly-after semantics of the interface.
		skMin := after.UTC().Format(timeutil.SimplifiedISO8601) + "$"
		input.KeyConditionExpression = aws.String("#pk = :pk AND #sk > :skMin")
		input.ExpressionAttributeNames["#sk"] = "sk"
		input.ExpressionAttributeValues[":skMin"] = &types.AttributeValueMemberS{Value: skMin}
	} else {
		input.KeyConditionExpression = aws.String("#pk = :pk")
	}

	out, err := s.dynamo.Query(ctx, input)
	if err != nil {
		return store.Page[spacediff.DifferenceRecord]{}, fmt.Errorf("listing space diffs: %w", err)
	}

	records := make([]spacediff.DifferenceRecord, 0, len(out.Items))
	for _, item := range out.Items {
		rec, err := itemToRecord(item)
		if err != nil {
			return store.Page[spacediff.DifferenceRecord]{}, err
		}
		records = append(records, rec)
	}

	var cursor *string
	if out.LastEvaluatedKey != nil {
		if skAttr, ok := out.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS); ok {
			cursor = &skAttr.Value
		}
	}

	return store.Page[spacediff.DifferenceRecord]{Results: records, Cursor: cursor}, nil
}

func compositeKey(a, b string) string {
	return a + "#" + b
}

func itemToRecord(item map[string]types.AttributeValue) (spacediff.DifferenceRecord, error) {
	providerAttr, ok := item["provider"].(*types.AttributeValueMemberS)
	if !ok {
		return spacediff.DifferenceRecord{}, fmt.Errorf("missing or invalid provider attribute")
	}
	provider, err := did.Parse(providerAttr.Value)
	if err != nil {
		return spacediff.DifferenceRecord{}, fmt.Errorf("parsing provider DID: %w", err)
	}

	spaceAttr, ok := item["space"].(*types.AttributeValueMemberS)
	if !ok {
		return spacediff.DifferenceRecord{}, fmt.Errorf("missing or invalid space attribute")
	}
	space, err := did.Parse(spaceAttr.Value)
	if err != nil {
		return spacediff.DifferenceRecord{}, fmt.Errorf("parsing space DID: %w", err)
	}

	subscriptionAttr, ok := item["subscription"].(*types.AttributeValueMemberS)
	if !ok {
		return spacediff.DifferenceRecord{}, fmt.Errorf("missing or invalid subscription attribute")
	}

	causeAttr, ok := item["cause"].(*types.AttributeValueMemberS)
	if !ok {
		return spacediff.DifferenceRecord{}, fmt.Errorf("missing or invalid cause attribute")
	}
	cause, err := cid.Parse(causeAttr.Value)
	if err != nil {
		return spacediff.DifferenceRecord{}, fmt.Errorf("parsing cause CID: %w", err)
	}

	deltaAttr, ok := item["delta"].(*types.AttributeValueMemberN)
	if !ok {
		return spacediff.DifferenceRecord{}, fmt.Errorf("missing or invalid delta attribute")
	}
	delta, err := strconv.ParseInt(deltaAttr.Value, 10, 64)
	if err != nil {
		return spacediff.DifferenceRecord{}, fmt.Errorf("parsing delta: %w", err)
	}

	receiptAtAttr, ok := item["receiptAt"].(*types.AttributeValueMemberS)
	if !ok {
		return spacediff.DifferenceRecord{}, fmt.Errorf("missing or invalid receiptAt attribute")
	}
	receiptAt, err := time.Parse(timeutil.SimplifiedISO8601, receiptAtAttr.Value)
	if err != nil {
		return spacediff.DifferenceRecord{}, fmt.Errorf("parsing receiptAt: %w", err)
	}

	var insertedAt time.Time
	if v, ok := item["insertedAt"].(*types.AttributeValueMemberS); ok {
		insertedAt, _ = time.Parse(timeutil.SimplifiedISO8601, v.Value)
	}

	return spacediff.DifferenceRecord{
		Provider:     provider,
		Space:        space,
		Subscription: subscriptionAttr.Value,
		Cause:        cause,
		Delta:        delta,
		ReceiptAt:    receiptAt,
		InsertedAt:   insertedAt,
	}, nil
}
