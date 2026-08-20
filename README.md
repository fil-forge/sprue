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

Sprue supports two store backends, selected by
`storage.type` (or `SPRUE_STORAGE_TYPE`; defaults to `postgres`):

- `memory` — in-process only; all data is lost on restart. Dev/test only.
- `postgres` — PostgreSQL for metadata + S3-compatible storage (MinIO, Ceph, AWS S3)
  for storing payloads of invocations, receipts, and delegations. Schema is managed by goose migrations embedded in
  `internal/migrations/sql/` and applied on startup.

## Logging

Sprue writes all logs to stdout/stderr through [zap](https://github.com/uber-go/zap).
HTTP request logs are routed through the same zap logger (via Echo's
`RequestLoggerWithConfig` middleware), so application and request logs share one
output and format.

The output format depends on the mode. By default sprue uses zap's production
configuration, which emits a single JSON stream. When the log level is `debug` or
the deployment environment is `development`/`test`, sprue uses zap's development
configuration, which emits a human-readable console format (not JSON).

The JSON output makes it straightforward to collect logs with a sidecar such as
Grafana Alloy or Promtail: point the collector at the container's stdout/stderr
and use a `json` pipeline stage to extract fields like `level`, `ts`, and `msg`.
Request logs carry `method`, `uri`, `status`, `latency`, `request_id`,
`content_length`, `response_size`, `headers`, and related fields, and use the
`request completed` / `client error` / `server error` messages.

## Notes

* Rate limits storage was not implemented. It has never been used in JS implementation, only supports blocking completely and can probably be applied at firewall.
* Plans, provisions, subscriptions, usage are not stores, they are services.
* Using `cid.Cid` in new code over `ipld.Link` to ease transition to UCAN 1.0 when it comes.
* `retrievalAuth` is now an array of CIDs - an explicit delegation chain.
* `/upload/add` now takes an optional `index` CID, allowing us to track/remove indexes.

## Container images

A push to `main` publishes to GHCR from the `Container` workflow. The `prod`
target becomes `ghcr.io/fil-forge/sprue:main`, a stripped binary on a slim
Debian base. The `dev` target becomes `ghcr.io/fil-forge/sprue:main-dev` and
adds delve plus a handful of debugging tools. Both cover `linux/amd64` and
`linux/arm64`, and both also carry a `sha-<short-sha>` tag, the dev image with a
`-dev` suffix.

## Deploying to dev

The same run asks [infra-central][] to deploy the prod image. It dispatches a
`bump-deployed-image` event carrying the manifest digest it just pushed, and
infra-central's [Bump deployed image][receiver] workflow opens a pull request
pinning that digest in `terraform/envs/dev/apps/terraform.tfvars`, with
auto-merge enabled. infra-central's [Check and deploy][deploy] workflow runs
`tofu apply` on `dev/apps` on every push to its `main`, so merging that pull
request is what deploys.

The dispatch runs as the `fil-forge-bot` GitHub App and needs the
`FORGE_BOT_APP_ID` variable and the `FORGE_BOT_PRIVATE_KEY` secret. Prod pins
are promoted by hand.

[infra-central]: https://github.com/fil-forge/infra-central
[receiver]: https://github.com/fil-forge/infra-central/blob/main/.github/workflows/bump-deployed-image.yml
[deploy]: https://github.com/fil-forge/infra-central/blob/main/.github/workflows/check-and-deploy.yml
