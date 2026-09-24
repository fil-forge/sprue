-- +goose Up
-- +goose StatementBegin
-- The object-count counterpart to space_diff: an append-only signed-delta log
-- of a space's uploads, so an object count at any past time is the cumulative
-- sum of deltas up to it. delta is +1 for an upload added and -1 for one
-- removed; space_diff's delta is bytes, which is why these are separate tables.
-- The primary key orders the log the way it is read (provider + space, then
-- receipt_at) and lets a replayed cause be skipped rather than double counted;
-- the writer asks for that with ON CONFLICT DO NOTHING.
CREATE TABLE upload_diff (
    provider     TEXT        NOT NULL,
    space        TEXT        NOT NULL,
    receipt_at   TIMESTAMPTZ NOT NULL,
    cause        TEXT        NOT NULL,
    subscription TEXT        NOT NULL,
    delta        BIGINT      NOT NULL,
    inserted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, space, receipt_at, cause)
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
INSERT INTO upload_diff (provider, space, receipt_at, cause, subscription, delta)
SELECT c.provider, u.space, u.inserted_at, u.cause, c.subscription, 1
FROM upload u
JOIN consumer c ON c.consumer = u.space
ON CONFLICT (provider, space, receipt_at, cause) DO NOTHING;

-- The counters the diff log is anchored on. '/upload/add-total' is
-- metrics.UploadAddTotalMetric, which is derived from the capability name and
-- so is stable. A space with no consumer is still counted here — it has no
-- provider to key a diff row by, and no query reaches it either, since usage
-- is always read per provider.
INSERT INTO space_metrics (space, name, value)
SELECT space, '/upload/add-total', count(*) FROM upload GROUP BY space
ON CONFLICT (space, name) DO UPDATE SET value = space_metrics.value + EXCLUDED.value;

INSERT INTO admin_metrics (name, value)
SELECT '/upload/add-total', count(*) FROM upload HAVING count(*) > 0
ON CONFLICT (name) DO UPDATE SET value = admin_metrics.value + EXCLUDED.value;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE upload_diff;
-- +goose StatementEnd
