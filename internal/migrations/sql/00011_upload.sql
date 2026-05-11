-- +goose Up
-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS upload_shard;
DROP TABLE IF EXISTS upload;
-- +goose StatementEnd
