-- +goose Up
-- +goose StatementBegin
ALTER TABLE customer         ALTER COLUMN inserted_at SET DEFAULT NOW();
ALTER TABLE storage_provider ALTER COLUMN inserted_at SET DEFAULT NOW();
ALTER TABLE consumer         ALTER COLUMN inserted_at SET DEFAULT NOW();
ALTER TABLE subscription     ALTER COLUMN inserted_at SET DEFAULT NOW();
ALTER TABLE space_diff       ALTER COLUMN inserted_at SET DEFAULT NOW();
ALTER TABLE replica          ALTER COLUMN inserted_at SET DEFAULT NOW();
ALTER TABLE revocation       ALTER COLUMN inserted_at SET DEFAULT NOW();
ALTER TABLE delegation       ALTER COLUMN inserted_at SET DEFAULT NOW();
ALTER TABLE upload           ALTER COLUMN inserted_at SET DEFAULT NOW();
ALTER TABLE blob_registry    ALTER COLUMN inserted_at SET DEFAULT NOW();
ALTER TABLE routing_policy   ALTER COLUMN inserted_at SET DEFAULT NOW();
ALTER TABLE space_routing    ALTER COLUMN inserted_at SET DEFAULT NOW();

-- updated_at is set by the database too, on insert here and by NOW() in every
-- update statement. customer.updated_at stays nullable with no default: NULL
-- means the row has never been updated.
ALTER TABLE storage_provider ALTER COLUMN updated_at SET DEFAULT NOW();
ALTER TABLE replica          ALTER COLUMN updated_at SET DEFAULT NOW();
ALTER TABLE delegation       ALTER COLUMN updated_at SET DEFAULT NOW();
ALTER TABLE upload           ALTER COLUMN updated_at SET DEFAULT NOW();
ALTER TABLE routing_policy   ALTER COLUMN updated_at SET DEFAULT NOW();
ALTER TABLE space_routing    ALTER COLUMN updated_at SET DEFAULT NOW();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE customer         ALTER COLUMN inserted_at DROP DEFAULT;
ALTER TABLE storage_provider ALTER COLUMN inserted_at DROP DEFAULT;
ALTER TABLE consumer         ALTER COLUMN inserted_at DROP DEFAULT;
ALTER TABLE subscription     ALTER COLUMN inserted_at DROP DEFAULT;
ALTER TABLE space_diff       ALTER COLUMN inserted_at DROP DEFAULT;
ALTER TABLE replica          ALTER COLUMN inserted_at DROP DEFAULT;
ALTER TABLE revocation       ALTER COLUMN inserted_at DROP DEFAULT;
ALTER TABLE delegation       ALTER COLUMN inserted_at DROP DEFAULT;
ALTER TABLE upload           ALTER COLUMN inserted_at DROP DEFAULT;
ALTER TABLE blob_registry    ALTER COLUMN inserted_at DROP DEFAULT;
ALTER TABLE routing_policy   ALTER COLUMN inserted_at DROP DEFAULT;
ALTER TABLE space_routing    ALTER COLUMN inserted_at DROP DEFAULT;
ALTER TABLE storage_provider ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE replica          ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE delegation       ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE upload           ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE routing_policy   ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE space_routing    ALTER COLUMN updated_at DROP DEFAULT;
-- +goose StatementEnd
