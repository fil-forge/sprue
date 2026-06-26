package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"go.uber.org/zap"

	"github.com/fil-forge/sprue/internal/config"
)

// A custom S3 endpoint (e.g. MinIO) must authenticate with the credentials from
// config, so each environment supplies its own access key rather than relying on
// a hard-coded default.
func TestNewS3ClientUsesConfiguredCredentials(t *testing.T) {
	cfg := config.S3Config{
		Endpoint:        "http://minio:9000",
		Region:          "us-east-1",
		AccessKeyID:     "staging-access-key",
		SecretAccessKey: "staging-secret-key",
	}

	client, err := NewS3Client(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("NewS3Client returned error: %v", err)
	}

	creds, err := client.Options().Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("retrieving credentials: %v", err)
	}

	got := aws.Credentials{AccessKeyID: creds.AccessKeyID, SecretAccessKey: creds.SecretAccessKey}
	want := aws.Credentials{AccessKeyID: cfg.AccessKeyID, SecretAccessKey: cfg.SecretAccessKey}
	if got != want {
		t.Errorf("S3 client credentials = %+v, want %+v (config-supplied, not hard-coded)", got, want)
	}
}

// A custom S3 endpoint requires explicit credentials, so an endpoint set without
// access key or secret fails fast with a clear error rather than surfacing an
// opaque auth failure on the first request.
func TestNewS3ClientRequiresCredentialsForCustomEndpoint(t *testing.T) {
	cases := map[string]config.S3Config{
		"missing both credentials":  {Endpoint: "http://minio:9000"},
		"missing access key id":     {Endpoint: "http://minio:9000", SecretAccessKey: "secret"},
		"missing secret access key": {Endpoint: "http://minio:9000", AccessKeyID: "key"},
	}

	for desc, cfg := range cases {
		t.Run(desc, func(t *testing.T) {
			_, err := NewS3Client(cfg, zap.NewNop())
			if err == nil {
				t.Fatal("NewS3Client returned nil error, want error about missing credentials")
			}
		})
	}
}

// Path-style addressing is controlled by storage.s3.use_path_style, decoupled
// from the endpoint setting, so it can be disabled even against a custom endpoint
// (and enabled against real AWS S3).
func TestNewS3ClientUsePathStyleFromConfig(t *testing.T) {
	cases := map[string]struct {
		cfg  config.S3Config
		want bool
	}{
		"enabled against a custom endpoint": {
			cfg:  config.S3Config{Endpoint: "http://minio:9000", AccessKeyID: "key", SecretAccessKey: "secret", UsePathStyle: true},
			want: true,
		},
		"disabled against a custom endpoint": {
			cfg:  config.S3Config{Endpoint: "http://minio:9000", AccessKeyID: "key", SecretAccessKey: "secret", UsePathStyle: false},
			want: false,
		},
		"enabled against real AWS S3": {
			cfg:  config.S3Config{UsePathStyle: true},
			want: true,
		},
	}

	for desc, tc := range cases {
		t.Run(desc, func(t *testing.T) {
			client, err := NewS3Client(tc.cfg, zap.NewNop())
			if err != nil {
				t.Fatalf("NewS3Client returned error: %v", err)
			}

			if got := client.Options().UsePathStyle; got != tc.want {
				t.Errorf("S3 client UsePathStyle = %v, want %v", got, tc.want)
			}
		})
	}
}
