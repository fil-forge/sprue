-- +goose Up
-- +goose StatementBegin
CREATE TABLE allocation (
    space       TEXT        NOT NULL,
    digest      TEXT        NOT NULL,
    size        BIGINT      NOT NULL,
    cause       TEXT        NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (space, digest)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS allocation;
-- +goose StatementEnd
