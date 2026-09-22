-- +goose Up
-- +goose StatementBegin
-- The object-count counterpart to space_diff: an append-only signed-delta log
-- of a space's uploads, so an object count at any past time is the cumulative
-- sum of deltas up to it. delta is +1 for an upload added and -1 for one
-- removed; space_diff's delta is bytes, which is why these are separate tables.
-- The primary key orders the log the way it is read (provider + space, then
-- receipt_at) and makes a replayed cause a no-op rather than a double count.
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE upload_diff;
-- +goose StatementEnd
