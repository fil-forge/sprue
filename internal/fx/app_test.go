package fx_test

import (
	"runtime"
	"testing"

	"github.com/fil-forge/sprue/internal/config"
	appfx "github.com/fil-forge/sprue/internal/fx"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/ucantone/multikey"
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
						PrivateKey: multikey.FormatSigner(testutil.WebServiceSigner),
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
							AccessKeyID:        "minioadmin",
							SecretAccessKey:    "minioadmin",
							UsePathStyle:       true,
							AgentMessageBucket: "agent-message-" + appID,
							DelegationBucket:   "delegation-" + appID,
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
						PrivateKey: multikey.FormatSigner(testutil.WebServiceSigner),
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
