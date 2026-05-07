package aws

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/internal/timeutil"
	"github.com/fil-forge/sprue/pkg/store"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
)

var DynamoStorageProviderTableProps = struct {
	KeySchema  []types.KeySchemaElement
	Attributes []types.AttributeDefinition
}{
	KeySchema: []types.KeySchemaElement{
		{
			AttributeName: aws.String("provider"),
			KeyType:       types.KeyTypeHash,
		},
	},
	Attributes: []types.AttributeDefinition{
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

var _ storageprovider.Store = (*Store)(nil)

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
		KeySchema:            DynamoStorageProviderTableProps.KeySchema,
		AttributeDefinitions: DynamoStorageProviderTableProps.Attributes,
		BillingMode:          types.BillingModePayPerRequest,
	}); err != nil {
		return fmt.Errorf("creating DynamoDB table %q: %w", s.tableName, err)
	}
	return nil
}

func (s *Store) Put(ctx context.Context, endpoint url.URL, proof delegation.Delegation, weight int, replicationWeight *int) error {
	proofStr, err := delegation.Format(proof)
	if err != nil {
		return fmt.Errorf("formatting proof: %w", err)
	}

	now := time.Now().UTC().Format(timeutil.SimplifiedISO8601)
	input := dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key:       map[string]types.AttributeValue{"provider": &types.AttributeValueMemberS{Value: proof.Issuer().DID().String()}},
		UpdateExpression: aws.String(
			"SET #endpoint = :endpoint, #proof = :proof, #weight = :weight, #replicationWeight = :replicationWeight, #insertedAt = if_not_exists(#insertedAt, :now), #updatedAt = :now",
		),
		ExpressionAttributeNames: map[string]string{
			"#endpoint":   "endpoint",
			"#proof":      "proof",
			"#weight":     "weight",
			"#insertedAt": "insertedAt",
			"#updatedAt":  "updatedAt",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":endpoint": &types.AttributeValueMemberS{Value: endpoint.String()},
			":proof":    &types.AttributeValueMemberS{Value: proofStr},
			":weight":   &types.AttributeValueMemberN{Value: strconv.Itoa(weight)},
			":now":      &types.AttributeValueMemberS{Value: now},
		},
	}
	if replicationWeight != nil {
		input.ExpressionAttributeNames["#replicationWeight"] = "replicationWeight"
		input.ExpressionAttributeValues[":replicationWeight"] = &types.AttributeValueMemberN{Value: strconv.Itoa(*replicationWeight)}
	}

	_, err = s.dynamo.UpdateItem(ctx, &input)
	if err != nil {
		return fmt.Errorf("storing storage provider: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, providerID did.DID) (storageprovider.Record, error) {
	out, err := s.dynamo.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tableName),
		Key:            map[string]types.AttributeValue{"provider": &types.AttributeValueMemberS{Value: providerID.String()}},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return storageprovider.Record{}, fmt.Errorf("getting storage provider: %w", err)
	}
	if len(out.Item) == 0 {
		return storageprovider.Record{}, storageprovider.ErrStorageProviderNotFound
	}
	return itemToRecord(out.Item)
}

func (s *Store) Delete(ctx context.Context, providerID did.DID) error {
	_, err := s.dynamo.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:           aws.String(s.tableName),
		Key:                 map[string]types.AttributeValue{"provider": &types.AttributeValueMemberS{Value: providerID.String()}},
		ConditionExpression: aws.String("attribute_exists(#provider)"),
		ExpressionAttributeNames: map[string]string{
			"#provider": "provider",
		},
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return storageprovider.ErrStorageProviderNotFound
		}
		return fmt.Errorf("deleting storage provider: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, options ...storageprovider.ListOption) (store.Page[storageprovider.Record], error) {
	cfg := storageprovider.ListConfig{}
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
			"provider": &types.AttributeValueMemberS{Value: *cfg.Cursor},
		}
	}

	out, err := s.dynamo.Scan(ctx, input)
	if err != nil {
		return store.Page[storageprovider.Record]{}, fmt.Errorf("listing storage providers: %w", err)
	}

	records := make([]storageprovider.Record, 0, len(out.Items))
	for _, item := range out.Items {
		rec, err := itemToRecord(item)
		if err != nil {
			return store.Page[storageprovider.Record]{}, err
		}
		records = append(records, rec)
	}

	var cursor *string
	if out.LastEvaluatedKey != nil {
		if providerAttr, ok := out.LastEvaluatedKey["provider"].(*types.AttributeValueMemberS); ok {
			cursor = &providerAttr.Value
		}
	}

	return store.Page[storageprovider.Record]{Results: records, Cursor: cursor}, nil
}

func itemToRecord(item map[string]types.AttributeValue) (storageprovider.Record, error) {
	providerAttr, ok := item["provider"].(*types.AttributeValueMemberS)
	if !ok {
		return storageprovider.Record{}, fmt.Errorf("missing or invalid provider attribute")
	}
	providerDID, err := did.Parse(providerAttr.Value)
	if err != nil {
		return storageprovider.Record{}, fmt.Errorf("parsing provider DID: %w", err)
	}

	endpointAttr, ok := item["endpoint"].(*types.AttributeValueMemberS)
	if !ok {
		return storageprovider.Record{}, fmt.Errorf("missing or invalid endpoint attribute")
	}
	endpointURL, err := url.Parse(endpointAttr.Value)
	if err != nil {
		return storageprovider.Record{}, fmt.Errorf("parsing endpoint URL: %w", err)
	}

	proofAttr, ok := item["proof"].(*types.AttributeValueMemberS)
	if !ok {
		return storageprovider.Record{}, fmt.Errorf("missing or invalid proof attribute")
	}
	proof, err := delegation.Parse(proofAttr.Value)
	if err != nil {
		return storageprovider.Record{}, fmt.Errorf("parsing proof: %w", err)
	}

	weightAttr, ok := item["weight"].(*types.AttributeValueMemberN)
	if !ok {
		return storageprovider.Record{}, fmt.Errorf("missing or invalid weight attribute")
	}
	weight, err := strconv.Atoi(weightAttr.Value)
	if err != nil {
		return storageprovider.Record{}, fmt.Errorf("parsing weight: %w", err)
	}

	var replicationWeight *int
	if item["replicationWeight"] != nil {
		replicationWeightAttr, ok := item["replicationWeight"].(*types.AttributeValueMemberN)
		if !ok {
			return storageprovider.Record{}, fmt.Errorf("missing or invalid replicationWeight attribute")
		}
		weight, err := strconv.Atoi(replicationWeightAttr.Value)
		if err != nil {
			return storageprovider.Record{}, fmt.Errorf("parsing replicationWeight: %w", err)
		}
		replicationWeight = &weight
	}

	rec := storageprovider.Record{
		Provider:          providerDID,
		Endpoint:          *endpointURL,
		Proof:             proof,
		Weight:            weight,
		ReplicationWeight: replicationWeight,
	}
	if v, ok := item["insertedAt"].(*types.AttributeValueMemberS); ok {
		rec.InsertedAt, _ = time.Parse(timeutil.SimplifiedISO8601, v.Value)
	}
	if v, ok := item["updatedAt"].(*types.AttributeValueMemberS); ok {
		rec.UpdatedAt, _ = time.Parse(timeutil.SimplifiedISO8601, v.Value)
	}
	return rec, nil
}
