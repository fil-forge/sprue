-- +goose Up
-- +goose StatementBegin
CREATE TABLE storage_provider (
    provider           TEXT        PRIMARY KEY,
    endpoint           TEXT        NOT NULL,
    weight             INTEGER     NOT NULL,
    replication_weight INTEGER,
    inserted_at        TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS storage_provider;
-- +goose StatementEnd
