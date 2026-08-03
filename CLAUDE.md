# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Run Commands

```bash
# Build the binary
go build -o sprue ./cmd/main.go

# Run the service
./sprue serve
./sprue serve -c config.yaml

# Run tests
go test ./...

# Run a specific test
go test ./pkg/service/... -run TestName -v

# Build Docker image
docker build -t sprue .
```

## Architecture Overview

Sprue is the upload coordination service for Storacha local development. It routes blob allocations to Piri storage nodes and tracks upload state in PostgreSQL.

### Core Components

**Dependency Injection (internal/fx/)**
- Uses uber-go/fx for dependency injection
- `AppModule` in `app.go` aggregates all modules: Config, Logger, Identity, Store, Clients, Service, Server
- Each module (e.g., `ConfigModule`, `ServerModule`) provides its dependencies via `fx.Provide` and hooks via `fx.Invoke`

**UCAN RPC Service (pkg/service/)**
- `Service` struct wraps a go-ucanto server that handles UCAN RPC requests
- Handlers in `pkg/service/handlers/` implement UCAN capabilities (e.g., `space/blob/add`, `upload/add`)
- Each handler follows the pattern: `With<Capability>Method(stores..., services..., logger) server.Option`
- Handlers receive their store and service dependencies directly as function parameters
- Handlers are registered via fx groups (`group:"ucan_options"`) and collected into the UCAN server

**Stores (pkg/store/)**
- Each domain has its own store interface in `pkg/store/<domain>/`
- Each store has two implementations: PostgreSQL (+ S3 for blob payloads) in `<domain>/postgres/` and in-memory in `<domain>/memory/`
- Store interfaces: `agent.Store`, `blob_registry.Store`, `consumer.Store`, `customer.Store`, `delegation.Store`, `metrics.Store`, `replica.Store`, `revocation.Store`, `space_diff.Store`, `storage_provider.Store`, `subscription.Store`, `upload.Store`
- Backends are wired in `internal/fx/store/<backend>/provider.go` (postgres, memory)
- Backend selection is driven by `storage.type` in config (`memory` | `postgres`; default `postgres`). Per-backend settings live under `storage.postgres` and `storage.s3`.
- Postgres schema is managed by goose migrations in `internal/migrations/sql/`, embedded and applied on startup. Set `storage.postgres.skip_migrations: true` to disable.

**Services (pkg/)**
- `provisioning`: Manages space provisioning (consumers + subscriptions)
- `routing`: Selects storage providers for blob allocation and replication
- `piriclient`: Communicates with Piri storage nodes for blob allocation/acceptance
- `indexerclient`: Communicates with the indexing service

**External Clients (pkg/)**
- `piriclient`: Communicates with Piri storage nodes for blob allocation/acceptance
- `indexerclient`: Communicates with the indexing service

### HTTP Endpoints (internal/fx/server.go)

- `GET /` - Service info (DID, version)
- `GET /health` - Health check
- `GET /.well-known/did.json` - DID document for did:web resolution
- `POST /` - UCAN RPC endpoint
- `GET /receipt/:cid` - Receipt retrieval

### Configuration

Configuration via YAML file or environment variables with `SPRUE_` prefix:
- `SPRUE_STORAGE_TYPE` — selects the store backend (`memory`, `postgres`; default `postgres`)
- `SPRUE_SERVER_HOST`, `SPRUE_SERVER_PORT`
- `SPRUE_IDENTITY_KEY_FILE`, `SPRUE_IDENTITY_PRIVATE_KEY`, `SPRUE_IDENTITY_SERVICE_DID`
- `SPRUE_INDEXER_ENDPOINT`
- `SPRUE_STORAGE_POSTGRES_DSN`, `SPRUE_STORAGE_POSTGRES_MAX_CONNS`, `SPRUE_STORAGE_POSTGRES_SKIP_MIGRATIONS`
- `SPRUE_STORAGE_S3_*` for S3/MinIO settings

### Key Dependencies

- **go-ucanto**: UCAN RPC framework for capability-based authorization
- **go-libstoracha**: Storacha capability definitions (blob, space, upload, etc.)
- **echo/v4**: HTTP server framework
- **aws-sdk-go-v2**: S3 client (blob payloads for the postgres backend)
- **jackc/pgx/v5**: PostgreSQL driver
- **pressly/goose/v3**: SQL schema migrations
- **viper/cobra**: Configuration and CLI
