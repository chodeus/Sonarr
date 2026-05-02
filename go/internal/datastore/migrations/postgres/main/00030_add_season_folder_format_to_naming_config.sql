-- Ports Migration/030. ADD COLUMN + cleanup; complex C# Config-table
-- substitution backfill is no-op for fresh DBs. See ledger row 30.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "NamingConfig" ADD COLUMN "SeasonFolderFormat" text;
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM "Config" WHERE "Key" = 'seasonfolderformat';
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM "Config" WHERE "Key" = 'useseasonfolder';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
