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

-- Rebuild each provider's totals from the changes that provider recorded, which
-- space_diff has held per provider all along. Copying the old space-wide total
-- to every current provider would instead hand a provider added part way
-- through a baseline of bytes it never served, which is the very thing keying
-- these totals by provider is meant to stop.
--
-- One diff row is one blob event, and the counters moved once per event, so the
-- log reconstructs them exactly. A zero-delta row counts as a store: nothing
-- distinguishes adding from removing an empty blob, and either way it
-- contributes no bytes. The metric names are the values of the constants in
-- pkg/store/metrics as of this migration.
INSERT INTO space_metrics_by_provider (provider, space, name, value)
SELECT provider, space, '/blob/add-total', COUNT(*)
FROM space_diff WHERE delta >= 0 GROUP BY provider, space
UNION ALL
SELECT provider, space, '/blob/add-size-total', COALESCE(SUM(delta), 0)
FROM space_diff WHERE delta >= 0 GROUP BY provider, space
UNION ALL
SELECT provider, space, '/blob/remove-total', COUNT(*)
FROM space_diff WHERE delta < 0 GROUP BY provider, space
UNION ALL
SELECT provider, space, '/blob/remove-size-total', -COALESCE(SUM(delta), 0)
FROM space_diff WHERE delta < 0 GROUP BY provider, space;

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

-- Collapsing back keeps one value per space. A space's providers can hold
-- different totals, since each counts only what it served, and the old
-- space-wide counter moved once per change however many providers saw it, so
-- the fullest history is the space's total rather than their sum.
INSERT INTO space_metrics_by_space (space, name, value)
SELECT space, name, MAX(value)
FROM space_metrics
GROUP BY space, name;

DROP TABLE space_metrics;
ALTER TABLE space_metrics_by_space RENAME TO space_metrics;
ALTER TABLE space_metrics RENAME CONSTRAINT space_metrics_by_space_pkey TO space_metrics_pkey;

-- +goose StatementEnd
