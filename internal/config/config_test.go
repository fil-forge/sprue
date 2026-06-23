package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// emptyConfigFile writes an empty config file in a temporary directory and
// returns its path. Passing an explicit file to Load isolates the test from any
// real config.yaml in the current working directory or /etc/sprue/, keeping the
// assertions focused on env/default behavior only.
func emptyConfigFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0o600))
	return path
}

// envOnlyCases covers keys that have no value in any config file and are set
// purely via environment variables. These previously had no viper default, so
// AutomaticEnv silently ignored them during Unmarshal.
var envOnlyCases = []struct {
	name   string
	envKey string
	envVal string
	get    func(*Config) any
	want   any
}{
	{"deployment.environment", "SPRUE_DEPLOYMENT_ENVIRONMENT", "production",
		func(c *Config) any { return c.Deployment.Environment }, "production"},
	{"deployment.allow_provision_without_payment_plan", "SPRUE_DEPLOYMENT_ALLOW_PROVISION_WITHOUT_PAYMENT_PLAN", "true",
		func(c *Config) any { return c.Deployment.AllowProvisionWithoutPaymentPlan }, true},
	{"deployment.max_replicas", "SPRUE_DEPLOYMENT_MAX_REPLICAS", "7",
		func(c *Config) any { return c.Deployment.MaxReplicas }, uint(7)},
	{"deployment.insecure_did_resolution", "SPRUE_DEPLOYMENT_INSECURE_DID_RESOLUTION", "true",
		func(c *Config) any { return c.Deployment.InsecureDIDResolution }, true},
	{"identity.service_did", "SPRUE_IDENTITY_SERVICE_DID", "did:web:upload",
		func(c *Config) any { return c.Identity.ServiceDID }, "did:web:upload"},
	{"identity.private_key", "SPRUE_IDENTITY_PRIVATE_KEY", "YOUR_KEY_HERE",
		func(c *Config) any { return c.Identity.PrivateKey }, "YOUR_KEY_HERE"},
	{"identity.key_file", "SPRUE_IDENTITY_KEY_FILE", "/etc/sprue/key.pem",
		func(c *Config) any { return c.Identity.KeyFile }, "/etc/sprue/key.pem"},
	{"server.public_url", "SPRUE_SERVER_PUBLIC_URL", "https://upload.example.com",
		func(c *Config) any { return c.Server.PublicURL }, "https://upload.example.com"},
	{"indexer.did", "SPRUE_INDEXER_DID", "did:web:idx",
		func(c *Config) any { return c.Indexer.DID }, "did:web:idx"},
	{"mailer.type", "SPRUE_MAILER_TYPE", "postmark",
		func(c *Config) any { return c.Mailer.Type }, "postmark"},
	{"mailer.sender", "SPRUE_MAILER_SENDER", "ops@example.com",
		func(c *Config) any { return c.Mailer.Sender }, "ops@example.com"},
	{"mailer.postmark_token", "SPRUE_MAILER_POSTMARK_TOKEN", "YOUR_TOKEN_HERE",
		func(c *Config) any { return c.Mailer.PostmarkToken }, "YOUR_TOKEN_HERE"},
	{"mailer.smtp_addr", "SPRUE_MAILER_SMTP_ADDR", "smtp.example.com:25",
		func(c *Config) any { return c.Mailer.SMTPAddr }, "smtp.example.com:25"},
}

func TestLoadHonorsEnvOnlyKeys(t *testing.T) {
	for _, tc := range envOnlyCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envKey, tc.envVal)

			cfg, err := Load(emptyConfigFile(t))
			assert.NoError(t, err)
			assert.Equal(t, tc.want, tc.get(cfg))
		})
	}
}

func TestLoadEmptyEnvOverridesDefault(t *testing.T) {
	// An explicitly empty env var must override the non-empty default, so that
	// e.g. the indexer can be disabled via env alone.
	t.Setenv("SPRUE_INDEXER_ENDPOINT", "")

	cfg, err := Load(emptyConfigFile(t))
	assert.NoError(t, err)
	assert.Equal(t, "", cfg.Indexer.Endpoint)
}
