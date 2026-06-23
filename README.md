# Sprue

The Forge upload service in Go (formerly the Storacha upload service).

## Running locally

The repo ships a `docker-compose.yaml` that brings up sprue alongside
PostgreSQL and MinIO for self-hosted development:

```bash
docker compose up -d postgres minio
SPRUE_STORAGE_POSTGRES_DSN="postgres://sprue:sprue@localhost:5432/sprue?sslmode=disable" \
  ./sprue serve
```

Postgres is the default store backend, so no extra flag is required.

## Store backends

Sprue supports three store backends, selected by
`storage.type` (or `SPRUE_STORAGE_TYPE`; defaults to `postgres`):

- `memory` — in-process only; all data is lost on restart. Dev/test only.
- `postgres` — PostgreSQL for metadata + S3-compatible storage (MinIO, Ceph, AWS S3)
  for storing payloads of invocations, receipts, and delegations. Schema is managed by goose migrations embedded in
  `internal/migrations/sql/` and applied on startup.
- `aws` — DynamoDB for metadata + S3 for storing payloads of invocations, receipts, and delegations.

## Logging

Sprue writes all logs to stdout/stderr as a single JSON stream produced by
[zap](https://github.com/uber-go/zap). HTTP request logs are routed through the
same zap logger (via Echo's `RequestLoggerWithConfig` middleware), so every log
line — application and request alike — shares one uniform JSON format.

This makes it straightforward to collect logs with a sidecar such as Grafana
Alloy or Promtail: point the collector at the container's stdout/stderr and use
a `json` pipeline stage to extract fields like `level`, `ts`, and `msg`. Request
logs carry `method`, `uri`, `status`, `latency`, `request_id`, and related
fields, and use the `REQUEST` / `REQUEST_ERROR` messages.

## Notes

* Rate limits storage was not implemented. It has never been used in JS implementation, only supports blocking completely and can probably be applied at firewall.
* Plans, provisions, subscriptions, usage are not stores, they are services.
* The following dynamo tables have GSIs that do not exist in w3infra that need to be added:
    * `consumer` - `consumerV3` and `customerV2`
* Using `cid.Cid` in new code over `ipld.Link` to ease transition to UCAN 1.0 when it comes.
* `retrievalAuth` is now an array of CIDs - an explicit delegation chain.
* `/upload/add` now takes an optional `index` CID, allowing us to track/remove indexes.
