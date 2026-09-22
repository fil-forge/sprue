-- +goose Up
-- +goose StatementBegin

-- Per-space metrics become per-provider, so they line up with space_diff, which
-- has always been keyed by provider. A change to a space writes one diff row per
-- provider, and now moves that provider's counters with it, so each provider's
-- counters balance its own rows. Without this a provider added after a space
-- held blobs reads a baseline it never stored.

CREATE TABLE space_metrics_by_provider (
    provider TEXT   NOT NULL,
    space    TEXT   NOT NULL,
    name     TEXT   NOT NULL,
    value    BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (provider, space, name)
);

-- Existing totals were counted once per change regardless of how many providers
-- served the space, which is what each individual provider recorded, so every
-- provider of the space inherits the whole value. A space with no consumer row
-- cannot have accrued metrics, since registering a blob requires one.
INSERT INTO space_metrics_by_provider (provider, space, name, value)
SELECT DISTINCT c.provider, m.space, m.name, m.value
FROM space_metrics m
JOIN consumer c ON c.consumer = m.space;

DROP TABLE space_metrics;
ALTER TABLE space_metrics_by_provider RENAME TO space_metrics;
-- A table rename leaves the primary key named after the table it was built as.
ALTER TABLE space_metrics RENAME CONSTRAINT space_metrics_by_provider_pkey TO space_metrics_pkey;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE space_metrics_by_space (
    space TEXT   NOT NULL,
    name  TEXT   NOT NULL,
    value BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (space, name)
);

-- Collapsing back keeps one value per space. The providers of a space hold the
-- same totals, so the largest is the space's total rather than their sum.
INSERT INTO space_metrics_by_space (space, name, value)
SELECT space, name, MAX(value)
FROM space_metrics
GROUP BY space, name;

DROP TABLE space_metrics;
ALTER TABLE space_metrics_by_space RENAME TO space_metrics;
ALTER TABLE space_metrics RENAME CONSTRAINT space_metrics_by_space_pkey TO space_metrics_pkey;

-- +goose StatementEnd
