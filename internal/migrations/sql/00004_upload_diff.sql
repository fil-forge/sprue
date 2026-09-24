-- +goose Up
-- +goose StatementBegin
-- The object-count counterpart to space_diff: an append-only signed-delta log
-- of a space's uploads, so an object count at any past time is the cumulative
-- sum of deltas up to it. delta is +1 for an upload added and -1 for one
-- removed; space_diff's delta is bytes, which is why these are separate tables.
--
-- Unlike space_diff this carries no provider or subscription. Bytes are billed
-- by the provider storing them, so that log is per provider; an object count is
-- a property of the space itself, and counting one object once per provider
-- would simply multiply it. A space's provider is established where it matters,
-- when the upload is added.
--
-- The primary key orders the log the way it is read (space, then receipt_at)
-- and lets a replayed cause be skipped rather than double counted; the writer
-- asks for that with ON CONFLICT DO NOTHING.
CREATE TABLE upload_diff (
    space       TEXT        NOT NULL,
    receipt_at  TIMESTAMPTZ NOT NULL,
    cause       TEXT        NOT NULL,
    delta       BIGINT      NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (space, receipt_at, cause)
);

-- Backfill the uploads this database already holds. Without it the counters
-- start at zero however many uploads exist, and removing a pre-existing one
-- records a -1 with no +1 to match, taking the reported count negative.
--
-- Each row is dated by the upload's own inserted_at rather than now(), so the
-- reconstructed series puts the object where it actually arrived instead of
-- spiking the whole history at upgrade. cause is the invocation that created
-- the upload, which is unique per (space, root), so the primary key cannot
-- collide; DO NOTHING only guards a re-run.
INSERT INTO upload_diff (space, receipt_at, cause, delta)
SELECT space, inserted_at, cause, 1 FROM upload
ON CONFLICT (space, receipt_at, cause) DO NOTHING;

-- The counters the diff log is anchored on. '/upload/add-total' is
-- metrics.UploadAddTotalMetric, which is derived from the capability name and
-- so is stable.
INSERT INTO space_metrics (space, name, value)
SELECT space, '/upload/add-total', count(*) FROM upload GROUP BY space
ON CONFLICT (space, name) DO UPDATE SET value = space_metrics.value + EXCLUDED.value;

INSERT INTO admin_metrics (name, value)
SELECT '/upload/add-total', count(*) FROM upload HAVING count(*) > 0
ON CONFLICT (name) DO UPDATE SET value = admin_metrics.value + EXCLUDED.value;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM space_metrics WHERE name = '/upload/add-total';
DELETE FROM admin_metrics WHERE name = '/upload/add-total';
DROP TABLE upload_diff;
-- +goose StatementEnd
