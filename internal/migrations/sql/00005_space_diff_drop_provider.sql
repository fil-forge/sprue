-- +goose Up
-- +goose StatementBegin

-- space_diff stops recording which provider served a change, and the
-- subscription that went with it. A change to a space is one event, and the log
-- holds one row for it, keyed by (space, receipt_at, cause).
--
-- Neither column was ever read. The usage service reads cause, delta and
-- receipt_at; the space counters are keyed by space alone.
--
-- Nor did they ever distinguish one row from another. Provisioning is
-- configured with a single allowed provider and rejects any other, and the
-- subscription is derived deterministically from the space DID, so a space
-- holds one consumer record and every change wrote exactly one row. The dedup
-- below therefore has nothing to collapse in any existing database; it is here
-- so the rebuild cannot fail on a shape the old schema permitted.

CREATE TABLE space_diff_by_space (
    space       TEXT        NOT NULL,
    receipt_at  TIMESTAMPTZ NOT NULL,
    cause       TEXT        NOT NULL,
    delta       BIGINT      NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (space, receipt_at, cause)
);

-- Rebuilt rather than altered in place: dropping provider takes the primary key
-- with it, and adding (space, receipt_at, cause) back is the statement that
-- would abort on the duplicates this is absorbing. The copy sets the column
-- order, the key, and the inserted_at default 00003 added, in one statement.
--
-- Were there ever two rows for one change, they would be that change seen from
-- each provider's side rather than parts of it, so one is kept. Summing them
-- would count the change twice and double the bytes the space is held to store,
-- which is what the usage service derives its series from.
--
-- DISTINCT ON, not SELECT DISTINCT: inserted_at is part of the row, and rows
-- written before 00003 took it from a clock read per statement, so two copies
-- could differ by microseconds, survive a full-row DISTINCT, and collide on the
-- new key instead.
INSERT INTO space_diff_by_space (space, receipt_at, cause, delta, inserted_at)
SELECT DISTINCT ON (space, receipt_at, cause)
       space, receipt_at, cause, delta, inserted_at
FROM space_diff
ORDER BY space, receipt_at, cause, inserted_at;

DROP TABLE space_diff;
ALTER TABLE space_diff_by_space RENAME TO space_diff;
-- A table rename leaves the primary key named after the table it was built as.
ALTER TABLE space_diff RENAME CONSTRAINT space_diff_by_space_pkey TO space_diff_pkey;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE space_diff_by_provider (
    provider     TEXT        NOT NULL,
    space        TEXT        NOT NULL,
    receipt_at   TIMESTAMPTZ NOT NULL,
    cause        TEXT        NOT NULL,
    subscription TEXT        NOT NULL,
    delta        BIGINT      NOT NULL,
    inserted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, space, receipt_at, cause)
);

-- The log stopped recording the provider and subscription, so they are read
-- back from the consumer records, which say which provider serves a space and
-- under which subscription, and which are only ever added to. A space holds one
-- consumer record, so this restores exactly what was dropped.
--
-- The join is a LEFT JOIN so a diff row whose space has no consumer record
-- aborts on provider's NOT NULL rather than vanishing: there is no DID to put
-- there that the service would read back, and a rollback should not quietly
-- drop a space's usage history.
INSERT INTO space_diff_by_provider (provider, space, receipt_at, cause, subscription, delta, inserted_at)
SELECT c.provider, d.space, d.receipt_at, d.cause, c.subscription, d.delta, d.inserted_at
FROM space_diff d
LEFT JOIN (
    SELECT DISTINCT ON (consumer, provider) consumer, provider, subscription
    FROM consumer
    ORDER BY consumer, provider, subscription
) c ON c.consumer = d.space;

DROP TABLE space_diff;
ALTER TABLE space_diff_by_provider RENAME TO space_diff;
ALTER TABLE space_diff RENAME CONSTRAINT space_diff_by_provider_pkey TO space_diff_pkey;

-- +goose StatementEnd
