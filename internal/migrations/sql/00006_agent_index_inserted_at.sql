-- +goose Up
-- +goose StatementBegin
-- agent_index records when each entry was written so old entries can be
-- purged by age. A re-written (task, kind) pair keeps its original time: the
-- column says when the row was created, and the writer's ON CONFLICT clause
-- only replaces token and message.
--
-- Rows that predate the column are dated at upgrade. There is no earlier
-- record of when they arrived, and the alternative of leaving them NULL
-- would exempt them from every purge.
ALTER TABLE agent_index ADD COLUMN inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- A purge reads the rows older than a cutoff, so the index is on the column
-- it ranges over and nothing else.
CREATE INDEX agent_index_inserted_at_idx ON agent_index (inserted_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX agent_index_inserted_at_idx;
ALTER TABLE agent_index DROP COLUMN inserted_at;
-- +goose StatementEnd
