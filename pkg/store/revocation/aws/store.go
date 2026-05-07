package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/sprue/pkg/internal/timeutil"
	"github.com/fil-forge/sprue/pkg/store/revocation"
	"github.com/ipfs/go-cid"
)

var DynamoRevocationTableProps = struct {
	KeySchema  []types.KeySchemaElement
	Attributes []types.AttributeDefinition
}{
	KeySchema: []types.KeySchemaElement{
		{
			AttributeName: aws.String("revoke"),
			KeyType:       types.KeyTypeHash,
		},
	},
	Attributes: []types.AttributeDefinition{
		{
			AttributeName: aws.String("revoke"),
			AttributeType: types.ScalarAttributeTypeS,
		},
	},
}

// batchGetSize is the maximum number of keys per BatchGetItem request.
const batchGetSize = 100

type Store struct {
	dynamo    *dynamodb.Client
	tableName string
}

var _ revocation.Store = (*Store)(nil)

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
		KeySchema:            DynamoRevocationTableProps.KeySchema,
		AttributeDefinitions: DynamoRevocationTableProps.Attributes,
		BillingMode:          types.BillingModePayPerRequest,
	}); err != nil {
		return fmt.Errorf("creating DynamoDB table %q: %w", s.tableName, err)
	}
	return nil
}

// Add adds a revocation for the given delegation and scope. If a revocation
// already exists for the same scope, it is left unchanged.
func (s *Store) Add(ctx context.Context, delegation cid.Cid, scope did.DID, cause cid.Cid) error {
	now := time.Now().UTC().Format(timeutil.SimplifiedISO8601)
	_, err := s.dynamo.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"revoke": &types.AttributeValueMemberS{Value: delegation.String()},
		},
		UpdateExpression: aws.String("SET #scope = if_not_exists(#scope, :val)"),
		ExpressionAttributeNames: map[string]string{
			"#scope": scope.String(),
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":val": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{
				"cause":      &types.AttributeValueMemberS{Value: cause.String()},
				"insertedAt": &types.AttributeValueMemberS{Value: now},
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("adding revocation: %w", err)
	}
	return nil
}

// Reset replaces all existing revocations for the given delegation with a
// single revocation for the given scope.
func (s *Store) Reset(ctx context.Context, delegation cid.Cid, scope did.DID, cause cid.Cid) error {
	now := time.Now().UTC().Format(timeutil.SimplifiedISO8601)
	_, err := s.dynamo.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item: map[string]types.AttributeValue{
			"revoke": &types.AttributeValueMemberS{Value: delegation.String()},
			scope.String(): &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{
				"cause":      &types.AttributeValueMemberS{Value: cause.String()},
				"insertedAt": &types.AttributeValueMemberS{Value: now},
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("resetting revocation: %w", err)
	}
	return nil
}

// Find returns revocations for the given delegation CIDs, keyed by delegation
// CID then scope DID.
func (s *Store) Find(ctx context.Context, delegations []cid.Cid) (map[cid.Cid]map[did.DID]cid.Cid, error) {
	if len(delegations) == 0 {
		return map[cid.Cid]map[did.DID]cid.Cid{}, nil
	}

	result := map[cid.Cid]map[did.DID]cid.Cid{}
	for i := 0; i < len(delegations); i += batchGetSize {
		end := min(i+batchGetSize, len(delegations))
		batch := delegations[i:end]

		keys := make([]map[string]types.AttributeValue, len(batch))
		for j, dlg := range batch {
			keys[j] = map[string]types.AttributeValue{
				"revoke": &types.AttributeValueMemberS{Value: dlg.String()},
			}
		}

		out, err := s.dynamo.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
			RequestItems: map[string]types.KeysAndAttributes{
				s.tableName: {Keys: keys},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("finding revocations: %w", err)
		}

		for _, item := range out.Responses[s.tableName] {
			dlgAttr, ok := item["revoke"].(*types.AttributeValueMemberS)
			if !ok {
				continue
			}
			dlgCID, err := cid.Parse(dlgAttr.Value)
			if err != nil {
				return nil, fmt.Errorf("parsing delegation CID %q: %w", dlgAttr.Value, err)
			}

			scopes := map[did.DID]cid.Cid{}
			for attrName, attrVal := range item {
				if attrName == "revoke" {
					continue
				}
				scope, err := did.Parse(attrName)
				if err != nil {
					// not a DID attribute — skip
					continue
				}
				scopeMap, ok := attrVal.(*types.AttributeValueMemberM)
				if !ok {
					continue
				}
				causeAttr, ok := scopeMap.Value["cause"].(*types.AttributeValueMemberS)
				if !ok {
					continue
				}
				cause, err := cid.Parse(causeAttr.Value)
				if err != nil {
					return nil, fmt.Errorf("parsing cause CID %q: %w", causeAttr.Value, err)
				}
				scopes[scope] = cause
			}
			if len(scopes) > 0 {
				result[dlgCID] = scopes
			}
		}
	}

	return result, nil
}
