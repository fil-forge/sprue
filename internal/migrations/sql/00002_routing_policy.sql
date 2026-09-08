-- +goose Up
-- +goose StatementBegin
CREATE TABLE routing_policy (
    policy      TEXT        PRIMARY KEY,
    candidates  TEXT[]      NOT NULL,
    cause       TEXT        NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE space_routing (
    space       TEXT        PRIMARY KEY,
    policy      TEXT        NOT NULL REFERENCES routing_policy (policy) ON DELETE CASCADE,
    cause       TEXT        NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_routing;
DROP TABLE IF EXISTS routing_policy;
-- +goose StatementEnd
