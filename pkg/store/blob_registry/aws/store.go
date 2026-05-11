package aws

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/fil-forge/libforge/capabilities/blob"
	"github.com/fil-forge/libforge/digestutil"
	"github.com/fil-forge/sprue/pkg/internal/timeutil"
	"github.com/fil-forge/sprue/pkg/store"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	metricsaws "github.com/fil-forge/sprue/pkg/store/metrics/aws"
	spacediffaws "github.com/fil-forge/sprue/pkg/store/space_diff/aws"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
	multihash "github.com/multiformats/go-multihash"
)

var DynamoBlobRegistryTableProps = struct {
	KeySchema  []types.KeySchemaElement
	Attributes []types.AttributeDefinition
	GSI        []types.GlobalSecondaryIndex
}{
	KeySchema: []types.KeySchemaElement{
		{
			AttributeName: aws.String("space"),
			KeyType:       types.KeyTypeHash,
		},
		{
			AttributeName: aws.String("digest"),
			KeyType:       types.KeyTypeRange,
		},
	},
	Attributes: []types.AttributeDefinition{
		{
			AttributeName: aws.String("space"),
			AttributeType: types.ScalarAttributeTypeS,
		},
		{
			AttributeName: aws.String("digest"),
			AttributeType: types.ScalarAttributeTypeS,
		},
	},
	GSI: []types.GlobalSecondaryIndex{
		{
			IndexName: aws.String("digest"),
			KeySchema: []types.KeySchemaElement{
				{
					AttributeName: aws.String("digest"),
					KeyType:       types.KeyTypeHash,
				},
				{
					AttributeName: aws.String("space"),
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
	dynamo        *dynamodb.Client
	tableName     string
	consumerStore consumer.Store
	spaceDiff     *spacediffaws.Store
	spaceMetrics  *metricsaws.SpaceStore
	adminMetrics  *metricsaws.Store
}

var _ blobregistry.Store = (*Store)(nil)

func New(dynamo *dynamodb.Client, tableName string, consumerStore consumer.Store, spaceDiff *spacediffaws.Store, spaceMetrics *metricsaws.SpaceStore, adminMetrics *metricsaws.Store) *Store {
	return &Store{
		dynamo:        dynamo,
		tableName:     tableName,
		consumerStore: consumerStore,
		spaceDiff:     spaceDiff,
		spaceMetrics:  spaceMetrics,
		adminMetrics:  adminMetrics,
	}
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
		KeySchema:              DynamoBlobRegistryTableProps.KeySchema,
		AttributeDefinitions:   DynamoBlobRegistryTableProps.Attributes,
		GlobalSecondaryIndexes: DynamoBlobRegistryTableProps.GSI,
		BillingMode:            types.BillingModePayPerRequest,
	}); err != nil {
		return fmt.Errorf("creating DynamoDB table %q: %w", s.tableName, err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, space did.DID, digest multihash.Multihash) (blobregistry.Record, error) {
	out, err := s.dynamo.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"space":  &types.AttributeValueMemberS{Value: space.String()},
			"digest": &types.AttributeValueMemberS{Value: digestutil.Format(digest)},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return blobregistry.Record{}, fmt.Errorf("getting blob registry entry: %w", err)
	}
	if len(out.Item) == 0 {
		return blobregistry.Record{}, blobregistry.ErrEntryNotFound
	}
	return itemToRecord(out.Item)
}

func (s *Store) Register(ctx context.Context, space did.DID, blob blob.Blob, cause cid.Cid) error {
	consumers, err := s.collectConsumers(ctx, space)
	if err != nil {
		return fmt.Errorf("collecting consumers: %w", err)
	}

	now := time.Now().UTC().Format(timeutil.SimplifiedISO8601)
	items := []types.TransactWriteItem{
		{
			Put: &types.Put{
				TableName: aws.String(s.tableName),
				Item: map[string]types.AttributeValue{
					"space":      &types.AttributeValueMemberS{Value: space.String()},
					"digest":     &types.AttributeValueMemberS{Value: digestutil.Format(blob.Digest)},
					"size":       &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", blob.Size)},
					"cause":      &types.AttributeValueMemberS{Value: cause.String()},
					"insertedAt": &types.AttributeValueMemberS{Value: now},
				},
				ConditionExpression: aws.String("attribute_not_exists(#space)"),
				ExpressionAttributeNames: map[string]string{
					"#space": "space",
				},
			},
		},
	}

	receiptAt := time.Now()
	for _, c := range consumers {
		items = append(items, s.spaceDiff.TransactPut(ctx, c.Provider, space, c.Subscription, cause, int64(blob.Size), receiptAt))
	}

	inc := map[string]uint64{
		metrics.BlobAddTotalMetric:     1,
		metrics.BlobAddSizeTotalMetric: blob.Size,
	}
	items = append(items, s.spaceMetrics.TransactIncrementTotals(space, inc)...)
	items = append(items, s.adminMetrics.TransactIncrementTotals(inc)...)

	if _, err := s.dynamo.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: items,
	}); err != nil {
		var txErr *types.TransactionCanceledException
		if errors.As(err, &txErr) {
			for _, reason := range txErr.CancellationReasons {
				if reason.Code != nil && *reason.Code == "ConditionalCheckFailed" {
					return blobregistry.ErrEntryExists
				}
			}
		}
		return fmt.Errorf("registering blob: %w", err)
	}
	return nil
}

func (s *Store) Deregister(ctx context.Context, space did.DID, digest multihash.Multihash, cause cid.Cid) error {
	existing, err := s.Get(ctx, space, digest)
	if err != nil {
		return err
	}

	consumers, err := s.collectConsumers(ctx, space)
	if err != nil {
		return fmt.Errorf("collecting consumers: %w", err)
	}

	items := []types.TransactWriteItem{
		{
			Delete: &types.Delete{
				TableName: aws.String(s.tableName),
				Key: map[string]types.AttributeValue{
					"space":  &types.AttributeValueMemberS{Value: space.String()},
					"digest": &types.AttributeValueMemberS{Value: digestutil.Format(digest)},
				},
				ConditionExpression: aws.String("attribute_exists(#space)"),
				ExpressionAttributeNames: map[string]string{
					"#space": "space",
				},
			},
		},
	}

	receiptAt := time.Now()
	for _, c := range consumers {
		items = append(items, s.spaceDiff.TransactPut(ctx, c.Provider, space, c.Subscription, cause, -int64(existing.Blob.Size), receiptAt))
	}

	inc := map[string]uint64{
		metrics.BlobRemoveTotalMetric:     1,
		metrics.BlobRemoveSizeTotalMetric: existing.Blob.Size,
	}
	items = append(items, s.spaceMetrics.TransactIncrementTotals(space, inc)...)
	items = append(items, s.adminMetrics.TransactIncrementTotals(inc)...)

	if _, err := s.dynamo.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: items,
	}); err != nil {
		var txErr *types.TransactionCanceledException
		if errors.As(err, &txErr) {
			for _, reason := range txErr.CancellationReasons {
				if reason.Code != nil && *reason.Code == "ConditionalCheckFailed" {
					return blobregistry.ErrEntryNotFound
				}
			}
		}
		return fmt.Errorf("deregistering blob: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, space did.DID, options ...blobregistry.ListOption) (store.Page[blobregistry.Record], error) {
	cfg := blobregistry.ListConfig{}
	for _, opt := range options {
		opt(&cfg)
	}

	input := &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("#space = :space"),
		ExpressionAttributeNames: map[string]string{
			"#space": "space",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":space": &types.AttributeValueMemberS{Value: space.String()},
		},
		ScanIndexForward: aws.Bool(true),
	}

	if cfg.Limit != nil {
		input.Limit = aws.Int32(int32(*cfg.Limit))
	}

	if cfg.Cursor != nil {
		input.ExclusiveStartKey = map[string]types.AttributeValue{
			"space":  &types.AttributeValueMemberS{Value: space.String()},
			"digest": &types.AttributeValueMemberS{Value: *cfg.Cursor},
		}
	}

	out, err := s.dynamo.Query(ctx, input)
	if err != nil {
		return store.Page[blobregistry.Record]{}, fmt.Errorf("listing blob registry entries: %w", err)
	}

	records := make([]blobregistry.Record, 0, len(out.Items))
	for _, item := range out.Items {
		rec, err := itemToRecord(item)
		if err != nil {
			return store.Page[blobregistry.Record]{}, err
		}
		records = append(records, rec)
	}

	var cursor *string
	if out.LastEvaluatedKey != nil {
		if digestAttr, ok := out.LastEvaluatedKey["digest"].(*types.AttributeValueMemberS); ok {
			cursor = &digestAttr.Value
		}
	}

	return store.Page[blobregistry.Record]{Results: records, Cursor: cursor}, nil
}

func (s *Store) collectConsumers(ctx context.Context, space did.DID) ([]consumer.Record, error) {
	results, err := store.Collect(ctx, func(ctx context.Context, options store.PaginationConfig) (store.Page[consumer.Record], error) {
		opts := []consumer.ListOption{}
		if options.Cursor != nil {
			opts = append(opts, consumer.WithListCursor(*options.Cursor))
		}
		return s.consumerStore.List(ctx, space, opts...)
	})
	if err != nil {
		return nil, fmt.Errorf("listing consumers: %w", err)
	}
	if len(results) == 0 {
		return nil, consumer.ErrConsumerNotFound
	}
	return results, nil
}

func itemToRecord(item map[string]types.AttributeValue) (blobregistry.Record, error) {
	spaceAttr, ok := item["space"].(*types.AttributeValueMemberS)
	if !ok {
		return blobregistry.Record{}, fmt.Errorf("missing or invalid space attribute")
	}
	space, err := did.Parse(spaceAttr.Value)
	if err != nil {
		return blobregistry.Record{}, fmt.Errorf("parsing space DID: %w", err)
	}

	digestAttr, ok := item["digest"].(*types.AttributeValueMemberS)
	if !ok {
		return blobregistry.Record{}, fmt.Errorf("missing or invalid digest attribute")
	}
	digest, err := digestutil.Parse(digestAttr.Value)
	if err != nil {
		return blobregistry.Record{}, fmt.Errorf("decoding digest: %w", err)
	}

	sizeAttr, ok := item["size"].(*types.AttributeValueMemberN)
	if !ok {
		return blobregistry.Record{}, fmt.Errorf("missing or invalid size attribute")
	}
	var size uint64
	if _, err := fmt.Sscanf(sizeAttr.Value, "%d", &size); err != nil {
		return blobregistry.Record{}, fmt.Errorf("parsing size: %w", err)
	}

	causeAttr, ok := item["cause"].(*types.AttributeValueMemberS)
	if !ok {
		return blobregistry.Record{}, fmt.Errorf("missing or invalid cause attribute")
	}
	cause, err := cid.Parse(causeAttr.Value)
	if err != nil {
		return blobregistry.Record{}, fmt.Errorf("parsing cause CID: %w", err)
	}

	var insertedAt time.Time
	if v, ok := item["insertedAt"].(*types.AttributeValueMemberS); ok {
		insertedAt, _ = time.Parse(timeutil.SimplifiedISO8601, v.Value)
	}

	return blobregistry.Record{
		Space: space,
		Blob: blob.Blob{
			Digest: digest,
			Size:   size,
		},
		Cause:      cause,
		InsertedAt: insertedAt,
	}, nil
}
