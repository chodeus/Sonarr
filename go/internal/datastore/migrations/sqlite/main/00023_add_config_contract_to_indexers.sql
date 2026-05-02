-- Ports Migration/023_add_config_contract_to_indexers.cs.
-- Backfills ConfigContract for known indexer Implementation strings, then
-- deletes any indexer row that didn't match (no ConfigContract set).
-- See go/analysis/migrations/ledger.md row 23.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "ConfigContract" = 'NewznabSettings' WHERE "Implementation" = 'Newznab';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Indexers" SET "ConfigContract" = 'OmgwtfnzbsSettings' WHERE "Implementation" = 'Omgwtfnzbs';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Indexers" SET "ConfigContract" = 'NullConfig' WHERE "Implementation" = 'Wombles';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Indexers" SET "ConfigContract" = 'NullConfig' WHERE "Implementation" = 'Eztv';
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM "Indexers" WHERE "ConfigContract" IS NULL;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
