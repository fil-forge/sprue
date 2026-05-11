package fx_test

import (
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/config"
	appfx "github.com/fil-forge/sprue/internal/fx"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/ucantone/principal/signer"
	"github.com/google/uuid"
	"go.uber.org/fx/fxtest"
)

// Test that the app can be wired with all modules and dependencies.
func TestWireApp(t *testing.T) {
	testCases := []struct {
		name      string
		configure func(t *testing.T) config.Config
	}{
		{
			name: "aws",
			configure: func(t *testing.T) config.Config {
				// This test expects docker to be running in linux CI environments and fails if it's not
				if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
					if !testutil.IsDockerAvailable(t) {
						t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
					}
				}
				// otherwise this test is running locally, skip it if docker isn't available
				if !testutil.IsDockerAvailable(t) {
					t.SkipNow()
				}

				dynamoEndpoint := testutil.CreateDynamo(t)
				s3Endpoint := testutil.CreateS3(t)
				appID := uuid.NewString()

				return config.Config{
					Deployment: config.DeploymentConfig{
						Environment: "test",
					},
					Server: config.ServerConfig{
						Host: "localhost",
						Port: 0,
					},
					Identity: config.IdentityConfig{
						PrivateKey: signer.Format(testutil.WebService),
						ServiceDID: testutil.WebService.DID().String(),
					},
					Indexer: config.IndexerConfig{
						Endpoint: "http://localhost:3000",
					},
					Storage: config.StorageConfig{
						Type: config.StorageTypeAWS,
						DynamoDB: config.DynamoDBConfig{
							Region:               "us-east-1",
							Endpoint:             dynamoEndpoint.String(),
							AgentIndexTable:      "agent-index-" + appID,
							BlobRegistryTable:    "blob-registry-" + appID,
							ConsumerTable:        "consumer-" + appID,
							CustomerTable:        "customer-" + appID,
							DelegationTable:      "delegation-" + appID,
							SpaceMetricsTable:    "space-metrics-" + appID,
							AdminMetricsTable:    "admin-metrics-" + appID,
							ReplicaTable:         "replica-" + appID,
							RevocationTable:      "revocation-" + appID,
							StorageProviderTable: "storage-provider-" + appID,
							SubscriptionTable:    "subscription-" + appID,
							SpaceDiffTable:       "space-diff-" + appID,
							UploadTable:          "upload-" + appID,
						},
						S3: config.S3Config{
							Region:             "us-east-1",
							Endpoint:           s3Endpoint.String(),
							AgentMessageBucket: "agent-message-" + appID,
							DelegationBucket:   "delegation-" + appID,
							UploadShardsBucket: "upload-shards-" + appID,
						},
					},
					Mailer: config.MailerConfig{
						Type: "nop",
					},
					Log: config.LogConfig{
						Level: "debug",
					},
				}
			},
		},
		{
			name: "postgres",
			configure: func(t *testing.T) config.Config {
				if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
					if !testutil.IsDockerAvailable(t) {
						t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
					}
				}
				if !testutil.IsDockerAvailable(t) {
					t.SkipNow()
				}
				pool := testutil.CreatePostgres(t)
				s3Endpoint := testutil.CreateS3(t)
				appID := uuid.NewString()

				return config.Config{
					Deployment: config.DeploymentConfig{
						Environment: "test",
					},
					Server: config.ServerConfig{
						Host: "localhost",
						Port: 0,
					},
					Identity: config.IdentityConfig{
						PrivateKey: signer.Format(testutil.WebService),
						ServiceDID: testutil.WebService.DID().String(),
					},
					Indexer: config.IndexerConfig{
						Endpoint: "http://localhost:3000",
					},
					Storage: config.StorageConfig{
						Type: config.StorageTypePostgres,
						Postgres: config.PostgresConfig{
							DSN:            pool.Config().ConnString(),
							SkipMigrations: true, // testutil.CreatePostgres already migrated
						},
						S3: config.S3Config{
							Region:             "us-east-1",
							Endpoint:           s3Endpoint.String(),
							AgentMessageBucket: "agent-message-" + appID,
							DelegationBucket:   "delegation-" + appID,
							UploadShardsBucket: "upload-shards-" + appID,
						},
					},
					Mailer: config.MailerConfig{
						Type: "nop",
					},
					Log: config.LogConfig{
						Level: "debug",
					},
				}
			},
		},
		{
			name: "memory",
			configure: func(t *testing.T) config.Config {
				return config.Config{
					Deployment: config.DeploymentConfig{
						Environment: "test",
					},
					Server: config.ServerConfig{
						Host: "localhost",
						Port: 0,
					},
					Identity: config.IdentityConfig{
						PrivateKey: signer.Format(testutil.WebService),
						ServiceDID: testutil.WebService.DID().String(),
					},
					Indexer: config.IndexerConfig{
						Endpoint: "http://localhost:3000",
					},
					Storage: config.StorageConfig{
						Type: config.StorageTypeMemory,
					},
					Mailer: config.MailerConfig{
						Type: "nop",
					},
					Log: config.LogConfig{
						Level: "debug",
					},
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.configure(t)
			app := fxtest.New(t, appfx.AppModule(&cfg))
			app.RequireStart()
			app.RequireStop()
		})
	}
}
