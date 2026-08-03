package testutil

import (
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"
)

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
