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

-- The diff log describes blob changes and nothing else, so a metric outside
-- the four above cannot be rebuilt from it and is carried across as it stands,
-- against each provider of the space. A space has one provider, so this keeps
-- the value rather than duplicating it. Dropping these instead would lose them
-- silently, and the store accepts any metric name.
INSERT INTO space_metrics_by_provider (provider, space, name, value)
SELECT DISTINCT c.provider, m.space, m.name, m.value
FROM space_metrics m
JOIN consumer c ON c.consumer = m.space
WHERE m.name NOT IN (
    '/blob/add-total',
    '/blob/add-size-total',
    '/blob/remove-total',
    '/blob/remove-size-total'
);

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

-- The old counters moved once per change however many providers saw it, so
-- collapsing back means counting changes rather than rows. A change is one
-- cause, which the providers that saw it share, so the diff log gives the old
-- values exactly the way it gives the new ones. Neither the sum across
-- providers (counting a shared change once per provider) nor the largest of
-- them (dropping what only a late provider saw) would.
INSERT INTO space_metrics_by_space (space, name, value)
SELECT space, '/blob/add-total', COUNT(*)
FROM (SELECT DISTINCT space, cause FROM space_diff WHERE delta >= 0) c GROUP BY space
UNION ALL
SELECT space, '/blob/add-size-total', COALESCE(SUM(delta), 0)
FROM (SELECT DISTINCT space, cause, delta FROM space_diff WHERE delta >= 0) c GROUP BY space
UNION ALL
SELECT space, '/blob/remove-total', COUNT(*)
FROM (SELECT DISTINCT space, cause FROM space_diff WHERE delta < 0) c GROUP BY space
UNION ALL
SELECT space, '/blob/remove-size-total', -COALESCE(SUM(delta), 0)
FROM (SELECT DISTINCT space, cause, delta FROM space_diff WHERE delta < 0) c GROUP BY space;

-- And the same for metrics the log cannot describe, collapsing the providers
-- of a space back to the one row the old schema held.
INSERT INTO space_metrics_by_space (space, name, value)
SELECT space, name, MAX(value)
FROM space_metrics
WHERE name NOT IN (
    '/blob/add-total',
    '/blob/add-size-total',
    '/blob/remove-total',
    '/blob/remove-size-total'
)
GROUP BY space, name;

DROP TABLE space_metrics;
ALTER TABLE space_metrics_by_space RENAME TO space_metrics;
ALTER TABLE space_metrics RENAME CONSTRAINT space_metrics_by_space_pkey TO space_metrics_pkey;

-- +goose StatementEnd
