package aws

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fil-forge/sprue/pkg/internal/timeutil"
	"github.com/fil-forge/sprue/pkg/store"
	dlgstore "github.com/fil-forge/sprue/pkg/store/delegation"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/delegation"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/ipfs/go-cid"
)

var DynamoDelegationTableProps = struct {
	KeySchema  []types.KeySchemaElement
	Attributes []types.AttributeDefinition
	GSI        []types.GlobalSecondaryIndex
}{
	KeySchema: []types.KeySchemaElement{
		{
			AttributeName: aws.String("link"),
			KeyType:       types.KeyTypeHash,
		},
	},
	Attributes: []types.AttributeDefinition{
		{
			AttributeName: aws.String("link"),
			AttributeType: types.ScalarAttributeTypeS,
		},
		{
			AttributeName: aws.String("audience"),
			AttributeType: types.ScalarAttributeTypeS,
		},
	},
	GSI: []types.GlobalSecondaryIndex{
		{
			IndexName: aws.String("audience"),
			KeySchema: []types.KeySchemaElement{
				{
					AttributeName: aws.String("audience"),
					KeyType:       types.KeyTypeHash,
				},
			},
			Projection: &types.Projection{
				ProjectionType:   types.ProjectionTypeInclude,
				NonKeyAttributes: []string{"link"},
			},
		},
	},
}

type Store struct {
	dynamo     *dynamodb.Client
	tableName  string
	s3         *s3.Client
	bucketName string
}

var _ dlgstore.Store = (*Store)(nil)

func New(dynamo *dynamodb.Client, tableName string, s3 *s3.Client, bucketName string) *Store {
	return &Store{
		dynamo:     dynamo,
		tableName:  tableName,
		s3:         s3,
		bucketName: bucketName,
	}
}

// Initialize creates the DynamoDB table and S3 bucket if they do not already exist.
func (s *Store) Initialize(ctx context.Context) error {
	if _, err := s.s3.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: &s.bucketName,
	}); err != nil {
		if _, err := s.s3.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: &s.bucketName,
		}); err != nil {
			return fmt.Errorf("creating S3 bucket %q: %w", s.bucketName, err)
		}
	}

	if _, err := s.dynamo.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(s.tableName),
	}); err != nil {
		if _, err := s.dynamo.CreateTable(ctx, &dynamodb.CreateTableInput{
			TableName:              aws.String(s.tableName),
			KeySchema:              DynamoDelegationTableProps.KeySchema,
			AttributeDefinitions:   DynamoDelegationTableProps.Attributes,
			GlobalSecondaryIndexes: DynamoDelegationTableProps.GSI,
			BillingMode:            types.BillingModePayPerRequest,
		}); err != nil {
			return fmt.Errorf("creating DynamoDB table %q: %w", s.tableName, err)
		}
	}
	return nil
}

func (s *Store) PutMany(ctx context.Context, tokens []ucan.Token, cause cid.Cid) error {
	now := time.Now().UTC().Format(timeutil.SimplifiedISO8601)
	for _, token := range tokens {
		link := token.Link().String()

		var body []byte
		var err error
		if dlg, ok := token.(ucan.Delegation); ok {
			body, err = delegation.Encode(dlg)
			if err != nil {
				return fmt.Errorf("encoding delegation %s: %w", link, err)
			}
		} else if inv, ok := token.(ucan.Invocation); ok {
			body, err = invocation.Encode(inv)
			if err != nil {
				return fmt.Errorf("encoding invocation %s: %w", link, err)
			}
		} else {
			return fmt.Errorf("unsupported token type: %T", token)
		}

		if _, err := s.s3.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &s.bucketName,
			Key:    aws.String(link),
			Body:   bytes.NewReader(body),
		}); err != nil {
			return fmt.Errorf("storing delegation %s in S3: %w", link, err)
		}

		var aud did.DID
		// audience may be nil if the token is an invocation
		if token.Audience() != nil {
			aud = token.Audience().DID()
		} else {
			aud = token.Subject().DID()
		}

		// Write the index entry to DynamoDB.
		item := map[string]types.AttributeValue{
			"link":       &types.AttributeValueMemberS{Value: link},
			"audience":   &types.AttributeValueMemberS{Value: aud.String()},
			"issuer":     &types.AttributeValueMemberS{Value: token.Issuer().DID().String()},
			"insertedAt": &types.AttributeValueMemberS{Value: now},
			"updatedAt":  &types.AttributeValueMemberS{Value: now},
		}
		if cause != cid.Undef {
			item["cause"] = &types.AttributeValueMemberS{Value: cause.String()}
		}
		if exp := token.Expiration(); exp != nil {
			item["expiration"] = &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", *exp)}
		}

		if _, err := s.dynamo.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(s.tableName),
			Item:      item,
		}); err != nil {
			return fmt.Errorf("indexing delegation %s in DynamoDB: %w", link, err)
		}
	}
	return nil
}

func (s *Store) ListByAudience(ctx context.Context, audience did.DID, options ...dlgstore.ListByAudienceOption) (store.Page[ucan.Token], error) {
	cfg := dlgstore.ListByAudienceConfig{}
	for _, opt := range options {
		opt(&cfg)
	}

	input := &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		IndexName:              aws.String("audience"),
		KeyConditionExpression: aws.String("#audience = :audience"),
		ExpressionAttributeNames: map[string]string{
			"#audience": "audience",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":audience": &types.AttributeValueMemberS{Value: audience.String()},
		},
	}
	if cfg.Limit != nil {
		input.Limit = aws.Int32(int32(*cfg.Limit))
	}
	if cfg.Cursor != nil {
		input.ExclusiveStartKey = map[string]types.AttributeValue{
			"audience": &types.AttributeValueMemberS{Value: audience.String()},
			"link":     &types.AttributeValueMemberS{Value: *cfg.Cursor},
		}
	}

	out, err := s.dynamo.Query(ctx, input)
	if err != nil {
		return store.Page[ucan.Token]{}, fmt.Errorf("querying delegations by audience: %w", err)
	}

	results := make([]ucan.Token, 0, len(out.Items))
	for _, item := range out.Items {
		linkAttr, ok := item["link"].(*types.AttributeValueMemberS)
		if !ok {
			return store.Page[ucan.Token]{}, fmt.Errorf("missing or invalid link attribute in DynamoDB item")
		}
		dlg, err := s.fetchToken(ctx, linkAttr.Value)
		if err != nil {
			return store.Page[ucan.Token]{}, fmt.Errorf("fetching delegation %s: %w", linkAttr.Value, err)
		}
		results = append(results, dlg)
	}

	var cursor *string
	if out.LastEvaluatedKey != nil {
		if linkAttr, ok := out.LastEvaluatedKey["link"].(*types.AttributeValueMemberS); ok {
			cursor = &linkAttr.Value
		}
	}

	return store.Page[ucan.Token]{Results: results, Cursor: cursor}, nil
}

// fetchToken retrieves and decodes a delegation/invocation from S3 by its link
// CID string.
func (s *Store) fetchToken(ctx context.Context, link string) (ucan.Token, error) {
	out, err := s.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucketName,
		Key:    aws.String(link),
	})
	if err != nil {
		return nil, fmt.Errorf("getting delegation from S3: %w", err)
	}
	defer out.Body.Close()

	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("reading delegation body from S3: %w", err)
	}

	inv, err := invocation.Decode(body)
	if err != nil {
		dlg, err := delegation.Decode(body)
		if err != nil {
			return nil, fmt.Errorf("decoding token: %w", err)
		}
		return dlg, nil
	}
	return inv, nil
}
