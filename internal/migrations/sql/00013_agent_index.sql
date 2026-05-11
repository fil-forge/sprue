-- +goose Up
-- +goose StatementBegin
-- agent_index stores a mapping from tasks to invocations/receipts and the
-- agent messages they were found in.
-- "kind" is the token type either "in" (an invocation) or "out" (a receipt).
-- "task" is CID of the task that was invoked (invocation) or ran (receipt).
-- "token" is the CID of the invocation/receipt.
-- "message" is the CID of the agent message the token can be found within.

CREATE TABLE agent_index (
    task    TEXT NOT NULL,
    kind    TEXT NOT NULL,
    token   TEXT NOT NULL,
    message TEXT NOT NULL, 
    PRIMARY KEY (task, kind)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_index;
-- +goose StatementEnd
