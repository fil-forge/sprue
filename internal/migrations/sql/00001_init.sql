-- +goose Up
-- +goose StatementBegin
CREATE TABLE customer (
    customer          TEXT        PRIMARY KEY,
    account           TEXT,
    product           TEXT        NOT NULL,
    details           JSONB,
    reserved_capacity BIGINT,
    inserted_at       TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ
);

CREATE INDEX customer_account_idx ON customer (account) WHERE account IS NOT NULL;

CREATE TABLE storage_provider (
    provider           TEXT        PRIMARY KEY,
    endpoint           TEXT        NOT NULL,
    weight             INTEGER     NOT NULL,
    replication_weight INTEGER,
    inserted_at        TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL
);

CREATE TABLE consumer (
    subscription TEXT        NOT NULL,
    provider     TEXT        NOT NULL,
    consumer     TEXT        NOT NULL,
    customer     TEXT        NOT NULL,
    cause        TEXT        NOT NULL,
    inserted_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (subscription, provider)
);

CREATE INDEX consumer_space_idx ON consumer (consumer, provider, subscription);
CREATE INDEX consumer_customer_idx ON consumer (customer, subscription, provider);

CREATE TABLE subscription (
    subscription TEXT        NOT NULL,
    provider     TEXT        NOT NULL,
    customer     TEXT        NOT NULL,
    cause        TEXT        NOT NULL,
    inserted_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (subscription, provider)
);

CREATE INDEX subscription_customer_provider_idx
    ON subscription (customer, provider, subscription);

CREATE TABLE admin_metrics (
    name  TEXT   PRIMARY KEY,
    value BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE space_metrics (
    space TEXT   NOT NULL,
    name  TEXT   NOT NULL,
    value BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (space, name)
);

CREATE TABLE space_diff (
    provider     TEXT        NOT NULL,
    space        TEXT        NOT NULL,
    receipt_at   TIMESTAMPTZ NOT NULL,
    cause        TEXT        NOT NULL,
    subscription TEXT        NOT NULL,
    delta        BIGINT      NOT NULL,
    inserted_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (provider, space, receipt_at, cause)
);

CREATE TABLE replica (
    space       TEXT        NOT NULL,
    digest      TEXT        NOT NULL,
    provider    TEXT        NOT NULL,
    status      TEXT        NOT NULL,
    cause       TEXT        NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (space, digest, provider)
);

CREATE TABLE revocation (
    revoke      TEXT        NOT NULL,
    scope       TEXT        NOT NULL,
    cause       TEXT        NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (revoke, scope)
);

CREATE TABLE delegation (
    link        TEXT        PRIMARY KEY,
    audience    TEXT        NOT NULL,
    issuer      TEXT        NOT NULL,
    cause       TEXT,
    expiration  BIGINT,
    inserted_at TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX delegation_audience_idx ON delegation (audience, link);

CREATE TABLE upload (
    space       TEXT        NOT NULL,
    root        TEXT        NOT NULL,
    index       TEXT,
    cause       TEXT        NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (space, root)
);

CREATE INDEX upload_root_idx ON upload (root);

CREATE TABLE upload_shard (
    space TEXT NOT NULL,
    root  TEXT NOT NULL,
    shard TEXT NOT NULL,
    PRIMARY KEY (space, root, shard),
    FOREIGN KEY (space, root) REFERENCES upload (space, root) ON DELETE CASCADE
);

CREATE TABLE blob_registry (
    space       TEXT        NOT NULL,
    digest      TEXT        NOT NULL,
    size        BIGINT      NOT NULL,
    cause       TEXT        NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (space, digest)
);

CREATE INDEX blob_registry_digest_idx ON blob_registry (digest, space);

-- agent_index stores a mapping from tasks to invocations/receipts and the
-- agent messages they were found in.
-- "kind" is the token type either "in" (an invocation) or "out" (a receipt).
-- "task" is CID of the task that was invoked (invocation) or ran (receipt).
-- "token" is the CID of the invocation/receipt.
-- "message" is the CID of the agent message the token can be found within.
CREATE TABLE agent_index (
    task    TEXT NOT NULL,
    kind    TEXT NOT NULL,
    token   TEXT NOT NULL,
    message TEXT NOT NULL,
    PRIMARY KEY (task, kind)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_index;
DROP TABLE IF EXISTS blob_registry;
DROP TABLE IF EXISTS upload_shard;
DROP TABLE IF EXISTS upload;
DROP TABLE IF EXISTS delegation;
DROP TABLE IF EXISTS revocation;
DROP TABLE IF EXISTS replica;
DROP TABLE IF EXISTS space_diff;
DROP TABLE IF EXISTS space_metrics;
DROP TABLE IF EXISTS admin_metrics;
DROP TABLE IF EXISTS subscription;
DROP TABLE IF EXISTS consumer;
DROP TABLE IF EXISTS storage_provider;
DROP TABLE IF EXISTS customer;
-- +goose StatementEnd
