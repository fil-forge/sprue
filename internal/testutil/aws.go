package testutil

import (
	"context"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcdynamodb "github.com/testcontainers/testcontainers-go/modules/dynamodb"
	"github.com/testcontainers/testcontainers-go/modules/minio"
)

func CreateDynamo(t *testing.T) *url.URL {
	ctx := context.Background()
	container, err := tcdynamodb.Run(ctx, "amazon/dynamodb-local:latest")
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)

	endpoint, err := container.ConnectionString(ctx)
	require.NoError(t, err)

	t.Logf("DynamoDB listening on: http://%s", endpoint)
	return Must(url.Parse("http://" + endpoint))(t)
}

func NewDynamoClient(t *testing.T, endpoint *url.URL) *dynamodb.Client {
	cfg, err := config.LoadDefaultConfig(
		context.Background(),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     "DUMMYIDEXAMPLE",
				SecretAccessKey: "DUMMYEXAMPLEKEY",
			},
		}),
		func(o *config.LoadOptions) error {
			o.Region = "us-east-1"
			o.RetryMaxAttempts = 10
			return nil
		},
	)

	require.NoError(t, err)
	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		base := endpoint.String()
		o.BaseEndpoint = &base
	})
}

func CreateS3(t *testing.T) *url.URL {
	container, err := minio.Run(t.Context(), "minio/minio:latest")
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)

	endpoint, err := container.ConnectionString(t.Context())
	require.NoError(t, err)

	t.Logf("S3 listening on: http://%s", endpoint)
	return Must(url.Parse("http://" + endpoint))(t)
}

func NewS3Client(t *testing.T, endpoint *url.URL) *s3.Client {
	cfg, err := config.LoadDefaultConfig(
		t.Context(),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     "minioadmin",
				SecretAccessKey: "minioadmin",
			},
		}),
		func(o *config.LoadOptions) error {
			o.Region = "us-east-1"
			return nil
		},
	)
	require.NoError(t, err)

	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		base := endpoint.String()
		o.BaseEndpoint = &base
		o.UsePathStyle = true
	})
}
