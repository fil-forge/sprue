package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Valid values for StorageConfig.Type.
const (
	StorageTypeMemory   = "memory"
	StorageTypePostgres = "postgres"
	StorageTypeAWS      = "aws"
)

// Config holds the sprue service configuration.
type Config struct {
	Deployment DeploymentConfig `mapstructure:"deployment"`
	Server     ServerConfig     `mapstructure:"server"`
	Identity   IdentityConfig   `mapstructure:"identity"`
	Indexer    IndexerConfig    `mapstructure:"indexer"`
	Storage    StorageConfig    `mapstructure:"storage"`
	Log        LogConfig        `mapstructure:"log"`
	Mailer     MailerConfig     `mapstructure:"mailer"`
}

type DeploymentConfig struct {
	// Environment is the deployment environment name (e.g., staging, production).
	Environment string `mapstructure:"environment"`
	// AllowProvisionWithoutPaymentPlan indicates whether the service allows users
	// to provision a space without an active payment plan. It should only be true
	// in development or testing environments.
	AllowProvisionWithoutPaymentPlan bool `mapstructure:"allow_provision_without_payment_plan"`
	// MaxReplicas is the maximum number of replicas that can be allocated for a
	// given blob. It includes the original blob that was uploaded, so only values
	// above 1 will allow users to have multiple copies of their data.
	MaxReplicas uint `mapstructure:"max_replicas"`
	// InsecureDIDResolution enables HTTP (instead of HTTPS) for did:web
	// resolution, which should only be used for development purposes.
	InsecureDIDResolution bool `mapstructure:"insecure_did_resolution" toml:"insecure_did_resolution,omitempty"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
	// PublicURL is the public URL for the service, used in email links and UCANs.
	// If not set, it will be derived from Host and Port.
	PublicURL string `mapstructure:"public_url"`
}

// IdentityConfig holds service identity settings.
type IdentityConfig struct {
	// KeyFile is the path to an Ed25519 PEM key file for service identity.
	// Takes precedence over PrivateKey if set.
	KeyFile string `mapstructure:"key_file"`

	// PrivateKey is the multibase base64-encoded ed25519 private key for service identity.
	// If empty and KeyFile is not set, a key will be generated at startup.
	PrivateKey string `mapstructure:"private_key"`

	// ServiceDID is the did:web identity for this service (e.g., did:web:upload).
	// When set with KeyFile, the service will wrap its did:key signer with this did:web
	// identity, allowing it to accept UCANs addressed to the did:web.
	ServiceDID string `mapstructure:"service_did"`
}

// IndexerConfig holds indexer service settings.
type IndexerConfig struct {
	// Endpoint is the URL of the indexing service.
	Endpoint string `mapstructure:"endpoint"`

	// DID is the DID of the indexer service (optional, can be derived from endpoint).
	DID string `mapstructure:"did"`
}

// StorageConfig selects and configures the store backend. Type picks which of
// Memory/Postgres/DynamoDB to use; S3 is shared by the postgres and aws
// backends for blob payload storage.
type StorageConfig struct {
	// Type selects the backend: "memory", "postgres", or "aws". Defaults to
	// "postgres".
	Type string `mapstructure:"type"`

	Memory   MemoryConfig   `mapstructure:"memory"`
	Postgres PostgresConfig `mapstructure:"postgres"`
	DynamoDB DynamoDBConfig `mapstructure:"dynamodb"`
	S3       S3Config       `mapstructure:"s3"`
}

// MemoryConfig configures the in-process store. It currently carries no
// settings but exists for symmetry with the persistent backends.
type MemoryConfig struct{}

// DynamoDBConfig holds DynamoDB settings.
type DynamoDBConfig struct {
	// Endpoint is the DynamoDB endpoint (for local development).
	Endpoint string `mapstructure:"endpoint"`

	// Region is the AWS region for DynamoDB.
	Region string `mapstructure:"region"`

	AgentIndexTable      string `mapstructure:"agent_index_table"`
	BlobRegistryTable    string `mapstructure:"blob_registry_table"`
	ConsumerTable        string `mapstructure:"consumer_table"`
	CustomerTable        string `mapstructure:"customer_table"`
	DelegationTable      string `mapstructure:"delegation_table"`
	SpaceMetricsTable    string `mapstructure:"space_metrics_table"`
	AdminMetricsTable    string `mapstructure:"admin_metrics_table"`
	ReplicaTable         string `mapstructure:"replica_table"`
	RevocationTable      string `mapstructure:"revocation_table"`
	StorageProviderTable string `mapstructure:"storage_provider_table"`
	SubscriptionTable    string `mapstructure:"subscription_table"`
	SpaceDiffTable       string `mapstructure:"space_diff_table"`
	UploadTable          string `mapstructure:"upload_table"`
}

// PostgresConfig holds PostgreSQL settings.
type PostgresConfig struct {
	// DSN is a libpq-style connection string, e.g.
	// "postgres://user:pass@host:5432/db?sslmode=disable".
	DSN string `mapstructure:"dsn"`
	// MaxConns is the maximum number of connections the pool will hold.
	MaxConns int32 `mapstructure:"max_conns"`
	// MinConns is the minimum number of idle connections the pool maintains.
	MinConns int32 `mapstructure:"min_conns"`
	// SkipMigrations disables automatic goose migrations on startup. Default
	// (false) runs migrations.
	SkipMigrations bool `mapstructure:"skip_migrations"`
}

// S3Config holds S3 settings.
type S3Config struct {
	// Endpoint is the S3 endpoint (for local development).
	Endpoint string `mapstructure:"endpoint"`

	// Region is the AWS region for S3.
	Region string `mapstructure:"region"`

	AgentMessageBucket string `mapstructure:"agent_message_bucket"`
	DelegationBucket   string `mapstructure:"delegation_bucket"`
	UploadShardsBucket string `mapstructure:"upload_shards_bucket"`
}

// LogConfig holds logging settings.
type LogConfig struct {
	// Level controls logging verbosity (debug, info, warn, error).
	Level string `mapstructure:"level"`
}

type MailerConfig struct {
	// Type specifies the mailer implementation to use (e.g., "postmark", "smtp", "nop").
	Type string `mapstructure:"type"`
	// Email address to use as the default sender for outgoing emails.
	Sender string `mapstructure:"sender"`
	// Subject configures the email subject line for outgoing emails. Note: this
	// is unused for some mailer types.
	Subject string `mapstructure:"subject"`
	// Postmark settings
	PostmarkToken string `mapstructure:"postmark_token"`
	// Address of the SMTP server (e.g., "smtp.example.com:25")
	SMTPAddr string `mapstructure:"smtp_addr"`
	// Username for SMTP authentication
	SMTPAuthUser string `mapstructure:"smtp_auth_user"`
	// Secret for CRAMMD5 SMTP authentication
	SMTPAuthSecret string `mapstructure:"smtp_auth_secret"`
}

// SetDefaults configures default values for viper.
//
// Every configurable key must be registered here, even with a zero-value
// default. viper's AutomaticEnv only binds an environment variable during
// Unmarshal when the key is already known to viper (via a default or a config
// file entry); keys with no default are silently ignored when set via env.
func SetDefaults(v *viper.Viper) {
	// Deployment defaults
	v.SetDefault("deployment.environment", "development")
	v.SetDefault("deployment.allow_provision_without_payment_plan", false)
	v.SetDefault("deployment.max_replicas", 3)
	v.SetDefault("deployment.insecure_did_resolution", false)

	// Server defaults
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.public_url", "")

	// Identity defaults (registered so env vars bind; empty means auto-generate)
	v.SetDefault("identity.key_file", "")
	v.SetDefault("identity.private_key", "")
	v.SetDefault("identity.service_did", "")

	// Indexer defaults (port 80 for did:web resolution in Docker)
	v.SetDefault("indexer.endpoint", "http://indexer:80")
	v.SetDefault("indexer.did", "")

	// Storage defaults — Postgres is the default backend.
	v.SetDefault("storage.type", StorageTypePostgres)

	// Postgres defaults
	v.SetDefault("storage.postgres.dsn", "postgres://sprue:sprue@postgres:5432/sprue?sslmode=disable")
	v.SetDefault("storage.postgres.max_conns", 10)
	v.SetDefault("storage.postgres.min_conns", 0)

	// DynamoDB defaults (only consulted when storage.type is "aws")
	v.SetDefault("storage.dynamodb.endpoint", "http://dynamodb-local:8000")
	v.SetDefault("storage.dynamodb.region", "us-west-1")
	v.SetDefault("storage.dynamodb.agent_index_table", "agent-index")
	v.SetDefault("storage.dynamodb.blob_registry_table", "blob-registry")
	v.SetDefault("storage.dynamodb.consumer_table", "consumer")
	v.SetDefault("storage.dynamodb.customer_table", "customer")
	v.SetDefault("storage.dynamodb.delegation_table", "delegation")
	v.SetDefault("storage.dynamodb.space_metrics_table", "space-metrics")
	v.SetDefault("storage.dynamodb.admin_metrics_table", "admin-metrics")
	v.SetDefault("storage.dynamodb.replica_table", "replica")
	v.SetDefault("storage.dynamodb.revocation_table", "revocation")
	v.SetDefault("storage.dynamodb.storage_provider_table", "storage-provider")
	v.SetDefault("storage.dynamodb.subscription_table", "subscription")
	v.SetDefault("storage.dynamodb.space_diff_table", "space-diff")
	v.SetDefault("storage.dynamodb.upload_table", "upload")

	// S3 defaults (used by the postgres and aws backends)
	v.SetDefault("storage.s3.endpoint", "http://minio:9000")
	v.SetDefault("storage.s3.region", "us-west-1")
	v.SetDefault("storage.s3.agent_message_bucket", "agent-message")
	v.SetDefault("storage.s3.delegation_bucket", "delegation")
	v.SetDefault("storage.s3.upload_shards_bucket", "upload-shards")

	// Log defaults
	v.SetDefault("log.level", "info")

	// Mailer defaults
	v.SetDefault("mailer.type", "nop")
	v.SetDefault("mailer.sender", "dev@storacha.network")
	v.SetDefault("mailer.subject", "")
	v.SetDefault("mailer.postmark_token", "")
	v.SetDefault("mailer.smtp_addr", "")
	v.SetDefault("mailer.smtp_auth_user", "")
	v.SetDefault("mailer.smtp_auth_secret", "")
}

// BindEnvVars sets up environment variable binding with SPRUE_ prefix.
func BindEnvVars(v *viper.Viper) {
	v.SetEnvPrefix("SPRUE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	// Treat an explicitly-set empty env var as a real value, so a non-empty
	// default (e.g. indexer.endpoint) can be cleared via the environment.
	v.AllowEmptyEnv(true)
	v.AutomaticEnv()
}

// Load creates a viper instance and loads configuration from the given config file
// (if provided), environment variables, and defaults.
func Load(configFile string) (*Config, error) {
	cfg, _, err := LoadWithViper(configFile)
	return cfg, err
}

// LoadWithViper creates a viper instance and loads configuration, returning both
// the Config struct and the viper instance for flag binding.
func LoadWithViper(configFile string) (*Config, *viper.Viper, error) {
	v := viper.New()

	SetDefaults(v)
	BindEnvVars(v)

	if configFile != "" {
		v.SetConfigFile(configFile)
		if err := v.ReadInConfig(); err != nil {
			return nil, nil, fmt.Errorf("reading config file: %w", err)
		}
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("/etc/sprue/")
		// Ignore error if no config file found - use defaults and env vars
		_ = v.ReadInConfig()
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	return &cfg, v, nil
}
