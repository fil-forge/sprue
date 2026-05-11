package aws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fil-forge/sprue/pkg/internal/timeutil"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/upload"
	"github.com/fil-forge/ucantone/did"
	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/codec/dagcbor"
	"github.com/ipld/go-ipld-prime/fluent"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/multiformats/go-multihash"
)

const (
	// ShardThreshold is the number of shards above which they are stored in S3
	// instead of DynamoDB.
	ShardThreshold = 5000
	// maxShardsPerPage is the maximum number of shards returned per page when
	// listing shards.
	maxShardsPerPage = 1000
)

var DynamoUploadTableProps = struct {
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
			AttributeName: aws.String("root"),
			KeyType:       types.KeyTypeRange,
		},
	},
	Attributes: []types.AttributeDefinition{
		{
			AttributeName: aws.String("space"),
			AttributeType: types.ScalarAttributeTypeS,
		},
		{
			AttributeName: aws.String("root"),
			AttributeType: types.ScalarAttributeTypeS,
		},
	},
	GSI: []types.GlobalSecondaryIndex{
		{
			IndexName: aws.String("cid"),
			KeySchema: []types.KeySchemaElement{
				{
					AttributeName: aws.String("root"),
					KeyType:       types.KeyTypeHash,
				},
			},
			Projection: &types.Projection{
				ProjectionType:   types.ProjectionTypeInclude,
				NonKeyAttributes: []string{"insertedAt"},
			},
		},
	},
}

type Store struct {
	dynamo     *dynamodb.Client
	s3         *s3.Client
	tableName  string
	bucketName string
}

var _ upload.Store = (*Store)(nil)

func New(dynamo *dynamodb.Client, tableName string, s3 *s3.Client, bucketName string) *Store {
	return &Store{dynamo: dynamo, s3: s3, tableName: tableName, bucketName: bucketName}
}

// Initialize creates the DynamoDB table and S3 bucket if they do not already exist.
func (d *Store) Initialize(ctx context.Context) error {
	var bucketExists bool
	if _, err := d.s3.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: &d.bucketName,
	}); err == nil {
		bucketExists = true
	}
	if !bucketExists {
		_, err := d.s3.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: &d.bucketName,
		})
		if err != nil {
			return fmt.Errorf("creating S3 bucket %q: %w", d.bucketName, err)
		}
	}

	var tableExists bool
	if _, err := d.dynamo.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(d.tableName),
	}); err == nil {
		tableExists = true
	}
	if !tableExists {
		if _, err := d.dynamo.CreateTable(ctx, &dynamodb.CreateTableInput{
			TableName:              aws.String(d.tableName),
			KeySchema:              DynamoUploadTableProps.KeySchema,
			AttributeDefinitions:   DynamoUploadTableProps.Attributes,
			GlobalSecondaryIndexes: DynamoUploadTableProps.GSI,
			BillingMode:            types.BillingModePayPerRequest,
		}); err != nil {
			return fmt.Errorf("creating DynamoDB table %q: %w", d.tableName, err)
		}
	}
	return nil
}

func (d *Store) Exists(ctx context.Context, space did.DID, root cid.Cid) (bool, error) {
	out, err := d.dynamo.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"space": &types.AttributeValueMemberS{Value: space.String()},
			"root":  &types.AttributeValueMemberS{Value: root.String()},
		},
		ProjectionExpression:     aws.String("#r"),
		ExpressionAttributeNames: map[string]string{"#r": "root"},
		ConsistentRead:           aws.Bool(true),
	})
	if err != nil {
		return false, fmt.Errorf("checking upload existence: %w", err)
	}
	return len(out.Item) > 0, nil
}

func (d *Store) Get(ctx context.Context, space did.DID, root cid.Cid) (upload.UploadRecord, error) {
	out, err := d.dynamo.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"space": &types.AttributeValueMemberS{Value: space.String()},
			"root":  &types.AttributeValueMemberS{Value: root.String()},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return upload.UploadRecord{}, fmt.Errorf("getting upload: %w", err)
	}
	if len(out.Item) == 0 {
		return upload.UploadRecord{}, upload.ErrUploadNotFound
	}
	return itemToRecord(out.Item)
}

func (d *Store) Inspect(ctx context.Context, root cid.Cid) (upload.UploadInspectRecord, error) {
	var spaces []did.DID
	var lastKey map[string]types.AttributeValue

	for {
		input := &dynamodb.QueryInput{
			TableName:              aws.String(d.tableName),
			IndexName:              aws.String("cid"),
			KeyConditionExpression: aws.String("#r = :root"),
			ExpressionAttributeNames: map[string]string{
				"#r": "root",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":root": &types.AttributeValueMemberS{Value: root.String()},
			},
			ExclusiveStartKey: lastKey,
		}

		out, err := d.dynamo.Query(ctx, input)
		if err != nil {
			return upload.UploadInspectRecord{}, fmt.Errorf("inspecting upload: %w", err)
		}

		for _, item := range out.Items {
			spaceAttr, ok := item["space"].(*types.AttributeValueMemberS)
			if !ok {
				continue
			}
			s, err := did.Parse(spaceAttr.Value)
			if err != nil {
				return upload.UploadInspectRecord{}, fmt.Errorf("parsing space DID: %w", err)
			}
			spaces = append(spaces, s)
		}

		lastKey = out.LastEvaluatedKey
		if lastKey == nil {
			break
		}
	}

	return upload.UploadInspectRecord{Spaces: spaces}, nil
}

func (d *Store) List(ctx context.Context, space did.DID, options ...upload.ListOption) (store.Page[upload.UploadRecord], error) {
	cfg := upload.ListConfig{}
	for _, option := range options {
		option(&cfg)
	}

	input := &dynamodb.QueryInput{
		TableName:              aws.String(d.tableName),
		KeyConditionExpression: aws.String("#s = :space"),
		ExpressionAttributeNames: map[string]string{
			"#s": "space",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":space": &types.AttributeValueMemberS{Value: space.String()},
		},
	}
	if cfg.Limit != nil {
		input.Limit = aws.Int32(int32(*cfg.Limit))
	}

	if cfg.Cursor != nil {
		input.ExclusiveStartKey = map[string]types.AttributeValue{
			"space": &types.AttributeValueMemberS{Value: space.String()},
			"root":  &types.AttributeValueMemberS{Value: *cfg.Cursor},
		}
	}

	out, err := d.dynamo.Query(ctx, input)
	if err != nil {
		return store.Page[upload.UploadRecord]{}, fmt.Errorf("listing uploads: %w", err)
	}

	records := make([]upload.UploadRecord, 0, len(out.Items))
	for _, item := range out.Items {
		r, err := itemToRecord(item)
		if err != nil {
			return store.Page[upload.UploadRecord]{}, err
		}
		records = append(records, r)
	}

	var cursor *string
	if out.LastEvaluatedKey != nil {
		if rootAttr, ok := out.LastEvaluatedKey["root"].(*types.AttributeValueMemberS); ok {
			cursor = &rootAttr.Value
		}
	}

	return store.Page[upload.UploadRecord]{Results: records, Cursor: cursor}, nil
}

func (d *Store) ListShards(ctx context.Context, space did.DID, root cid.Cid, options ...upload.ListShardsOption) (store.Page[cid.Cid], error) {
	cfg := upload.ListShardsConfig{}
	for _, option := range options {
		option(&cfg)
	}
	if cfg.Limit == nil || *cfg.Limit > maxShardsPerPage {
		cfg.Limit = aws.Int(maxShardsPerPage)
	}

	out, err := d.dynamo.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"space": &types.AttributeValueMemberS{Value: space.String()},
			"root":  &types.AttributeValueMemberS{Value: root.String()},
		},
		ProjectionExpression: aws.String("shards, shardsRef"),
	})
	if err != nil {
		return store.Page[cid.Cid]{}, fmt.Errorf("getting shards: %w", err)
	}
	if len(out.Item) == 0 {
		return store.Page[cid.Cid]{}, nil
	}

	allShards, err := d.fetchShards(ctx, out.Item)
	if err != nil {
		return store.Page[cid.Cid]{}, err
	}

	shards := allShards
	if cfg.Cursor != nil {
		for i, s := range allShards {
			if s.String() == *cfg.Cursor {
				shards = allShards[i+1:]
				break
			}
		}
	}

	if len(shards) > *cfg.Limit {
		shards = shards[:*cfg.Limit]
	}

	var cursor *string
	if len(shards) > 0 && len(shards) < len(allShards) {
		c := shards[len(shards)-1].String()
		cursor = &c
	}

	return store.Page[cid.Cid]{Results: shards, Cursor: cursor}, nil
}

func (d *Store) Remove(ctx context.Context, space did.DID, root cid.Cid) error {
	out, err := d.dynamo.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"space": &types.AttributeValueMemberS{Value: space.String()},
			"root":  &types.AttributeValueMemberS{Value: root.String()},
		},
		ConditionExpression:      aws.String("attribute_exists(#r)"),
		ExpressionAttributeNames: map[string]string{"#r": "root"},
		ReturnValues:             types.ReturnValueAllOld,
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return upload.ErrUploadNotFound
		}
		return fmt.Errorf("removing upload: %w", err)
	}
	if shardsRefAttr, ok := out.Attributes["shardsRef"].(*types.AttributeValueMemberS); ok {
		if _, err := d.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(d.bucketName),
			Key:    aws.String(shardsRefAttr.Value),
		}); err != nil {
			return fmt.Errorf("removing shards from S3: %w", err)
		}
	}
	return nil
}

func (d *Store) Upsert(ctx context.Context, space did.DID, root cid.Cid, index *cid.Cid, shards []cid.Cid, cause cid.Cid) error {
	// Fetch the current item to get existing shards before merging.
	current, err := d.dynamo.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"space": &types.AttributeValueMemberS{Value: space.String()},
			"root":  &types.AttributeValueMemberS{Value: root.String()},
		},
		ProjectionExpression: aws.String("shards, shardsRef"),
		ConsistentRead:       aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("fetching current upload: %w", err)
	}

	var existing []cid.Cid
	var oldShardsRef string
	if len(current.Item) > 0 {
		existing, err = d.fetchShards(ctx, current.Item)
		if err != nil {
			return err
		}
		if v, ok := current.Item["shardsRef"].(*types.AttributeValueMemberS); ok {
			oldShardsRef = v.Value
		}
	}

	merged := mergeShards(existing, shards)

	now := time.Now().UTC().Format(timeutil.SimplifiedISO8601)
	exprAttrNames := map[string]string{
		"#insertedAt": "insertedAt",
		"#updatedAt":  "updatedAt",
		"#cause":      "cause",
		"#index":      "index",
		"#shardsRef":  "shardsRef",
		"#shards":     "shards",
	}
	exprAttrValues := map[string]types.AttributeValue{
		":now":   &types.AttributeValueMemberS{Value: now},
		":cause": &types.AttributeValueMemberS{Value: cause.String()},
	}
	if index != nil {
		exprAttrValues[":index"] = &types.AttributeValueMemberS{Value: index.String()}
	}

	var updateExpr string
	var newShardsRef string
	if len(merged) >= ShardThreshold {
		link, data, err := encodeShards(merged)
		if err != nil {
			return err
		}
		newShardsRef = fmt.Sprintf("%s/%s", space.String(), link.String())
		if _, err := d.s3.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(d.bucketName),
			Key:    aws.String(newShardsRef),
			Body:   bytes.NewReader(data),
		}); err != nil {
			return fmt.Errorf("storing shards in S3: %w", err)
		}
		exprAttrValues[":shardsRef"] = &types.AttributeValueMemberS{Value: newShardsRef}
		if index != nil {
			updateExpr = "SET #insertedAt = if_not_exists(#insertedAt, :now), #updatedAt = :now, #cause = :cause, #shardsRef = :shardsRef, #index = :index REMOVE #shards"
		} else {
			updateExpr = "SET #insertedAt = if_not_exists(#insertedAt, :now), #updatedAt = :now, #cause = :cause, #shardsRef = :shardsRef REMOVE #shards, #index"
		}
	} else if len(merged) > 0 {
		shardStrs := make([]string, len(merged))
		for i, s := range merged {
			shardStrs[i] = s.String()
		}
		exprAttrValues[":shards"] = &types.AttributeValueMemberSS{Value: shardStrs}
		if index != nil {
			updateExpr = "SET #insertedAt = if_not_exists(#insertedAt, :now), #updatedAt = :now, #cause = :cause, #shards = :shards, #index = :index REMOVE #shardsRef"
		} else {
			updateExpr = "SET #insertedAt = if_not_exists(#insertedAt, :now), #updatedAt = :now, #cause = :cause, #shards = :shards REMOVE #shardsRef, #index"
		}
	} else {
		if index != nil {
			updateExpr = "SET #insertedAt = if_not_exists(#insertedAt, :now), #updatedAt = :now, #cause = :cause, #index = :index REMOVE #shards, #shardsRef"
		} else {
			updateExpr = "SET #insertedAt = if_not_exists(#insertedAt, :now), #updatedAt = :now, #cause = :cause REMOVE #shards, #shardsRef, #index"
		}
	}

	_, err = d.dynamo.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"space": &types.AttributeValueMemberS{Value: space.String()},
			"root":  &types.AttributeValueMemberS{Value: root.String()},
		},
		UpdateExpression:          aws.String(updateExpr),
		ExpressionAttributeNames:  exprAttrNames,
		ExpressionAttributeValues: exprAttrValues,
	})
	if err != nil {
		return fmt.Errorf("upserting upload: %w", err)
	}
	// If the shards were previously stored in S3 under a different key, delete
	// the old object now that DynamoDB points to the new one (or none at all).
	if oldShardsRef != "" && oldShardsRef != newShardsRef {
		if _, err := d.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(d.bucketName),
			Key:    aws.String(oldShardsRef),
		}); err != nil {
			return fmt.Errorf("removing old shards from S3: %w", err)
		}
	}
	return nil
}

// fetchShards returns shards from the DynamoDB item, reading from S3 if a
// shardsRef attribute is present.
func (d *Store) fetchShards(ctx context.Context, item map[string]types.AttributeValue) ([]cid.Cid, error) {
	if shardsRefAttr, ok := item["shardsRef"].(*types.AttributeValueMemberS); ok {
		return d.fetchShardsFromS3(ctx, shardsRefAttr.Value)
	}
	return itemToShards(item)
}

func (d *Store) fetchShardsFromS3(ctx context.Context, key string) ([]cid.Cid, error) {
	out, err := d.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(d.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("fetching shards from S3 (%s): %w", key, err)
	}
	defer out.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(out.Body); err != nil {
		return nil, fmt.Errorf("reading shards from S3 (%s): %w", key, err)
	}
	return decodeShards(buf.Bytes())
}

// mergeShards returns existing with any incoming shards appended that are not
// already present.
func mergeShards(existing, incoming []cid.Cid) []cid.Cid {
	seen := make(map[string]struct{}, len(existing))
	for _, s := range existing {
		seen[s.String()] = struct{}{}
	}
	result := make([]cid.Cid, len(existing), len(existing)+len(incoming))
	copy(result, existing)
	for _, s := range incoming {
		if _, ok := seen[s.String()]; !ok {
			seen[s.String()] = struct{}{}
			result = append(result, s)
		}
	}
	slices.SortFunc(result, func(a, b cid.Cid) int {
		return bytes.Compare(a.Bytes(), b.Bytes())
	})
	return result
}

func itemToRecord(item map[string]types.AttributeValue) (upload.UploadRecord, error) {
	spaceAttr, ok := item["space"].(*types.AttributeValueMemberS)
	if !ok {
		return upload.UploadRecord{}, fmt.Errorf("missing or invalid space attribute")
	}
	space, err := did.Parse(spaceAttr.Value)
	if err != nil {
		return upload.UploadRecord{}, fmt.Errorf("parsing space DID: %w", err)
	}

	rootAttr, ok := item["root"].(*types.AttributeValueMemberS)
	if !ok {
		return upload.UploadRecord{}, fmt.Errorf("missing or invalid root attribute")
	}
	root, err := cid.Parse(rootAttr.Value)
	if err != nil {
		return upload.UploadRecord{}, fmt.Errorf("parsing root CID: %w", err)
	}

	var index *cid.Cid
	if _, ok := item["index"]; ok {
		indexAttr, ok := item["index"].(*types.AttributeValueMemberS)
		if !ok {
			return upload.UploadRecord{}, fmt.Errorf("missing or invalid index attribute")
		}
		c, err := cid.Parse(indexAttr.Value)
		if err != nil {
			return upload.UploadRecord{}, fmt.Errorf("parsing index CID: %w", err)
		}
		index = &c
	}

	causeAttr, ok := item["cause"].(*types.AttributeValueMemberS)
	if !ok {
		return upload.UploadRecord{}, fmt.Errorf("missing or invalid cause attribute")
	}
	cause, err := cid.Parse(causeAttr.Value)
	if err != nil {
		return upload.UploadRecord{}, fmt.Errorf("parsing cause CID: %w", err)
	}

	var insertedAt time.Time
	if v, ok := item["insertedAt"].(*types.AttributeValueMemberS); ok {
		insertedAt, err = time.Parse(timeutil.SimplifiedISO8601, v.Value)
		if err != nil {
			return upload.UploadRecord{}, fmt.Errorf("parsing insertedAt: %w", err)
		}
	}

	var updatedAt time.Time
	if v, ok := item["updatedAt"].(*types.AttributeValueMemberS); ok {
		updatedAt, _ = time.Parse(timeutil.SimplifiedISO8601, v.Value)
	}

	return upload.UploadRecord{
		Space:      space,
		Root:       root,
		Index:      index,
		Cause:      cause,
		InsertedAt: insertedAt,
		UpdatedAt:  updatedAt,
	}, nil
}

func itemToShards(item map[string]types.AttributeValue) ([]cid.Cid, error) {
	shardsAttr, ok := item["shards"]
	if !ok {
		return nil, nil
	}
	ss, ok := shardsAttr.(*types.AttributeValueMemberSS)
	if !ok {
		return nil, nil
	}
	links := make([]cid.Cid, 0, len(ss.Value))
	for _, s := range ss.Value {
		c, err := cid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("parsing shard CID %s: %w", s, err)
		}
		links = append(links, c)
	}
	return links, nil
}

func encodeShards(shards []cid.Cid) (cid.Cid, []byte, error) {
	n, err := fluent.BuildList(basicnode.Prototype.List, int64(len(shards)), func(la fluent.ListAssembler) {
		for _, s := range shards {
			la.AssembleValue().AssignLink(cidlink.Link{Cid: s})
		}
	})
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("encoding shards: %w", err)
	}
	var buf bytes.Buffer
	if err := dagcbor.Encode(n, &buf); err != nil {
		return cid.Undef, nil, fmt.Errorf("encoding shards to DAG-CBOR: %w", err)
	}
	c, err := cid.Prefix{
		Version:  1,
		Codec:    cid.DagCBOR,
		MhType:   multihash.SHA2_256,
		MhLength: -1,
	}.Sum(buf.Bytes())
	if err != nil {
		return cid.Undef, nil, fmt.Errorf("creating CID for shards: %w", err)
	}
	return c, buf.Bytes(), nil
}

func decodeShards(data []byte) ([]cid.Cid, error) {
	r := bytes.NewReader(data)
	nb := basicnode.Prototype.List.NewBuilder()
	err := dagcbor.Decode(nb, r)
	if err != nil {
		return nil, fmt.Errorf("decoding shards from DAG-CBOR: %w", err)
	}
	n := nb.Build()
	shards := []cid.Cid{}
	iter := n.ListIterator()
	for !iter.Done() {
		_, shardNode, err := iter.Next()
		if err != nil {
			return nil, fmt.Errorf("iterating over shards list: %w", err)
		}
		sl, err := shardNode.AsLink()
		if err != nil {
			return nil, fmt.Errorf("getting shard link: %w", err)
		}
		var shard cid.Cid
		if cl, ok := sl.(cidlink.Link); ok {
			shard = cl.Cid
		} else {
			shard, err = cid.Parse(sl.String())
			if err != nil {
				return nil, fmt.Errorf("parsing shard CID: %w", err)
			}
		}
		shards = append(shards, shard)
	}
	return shards, nil
}
