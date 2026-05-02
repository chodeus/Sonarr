-- Ports Migration/035. ADD COLUMN + UPDATE all rows with default. See ledger row 35.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "NamingConfig" ADD COLUMN "SeriesFolderFormat" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "NamingConfig" SET "SeriesFolderFormat" = '{Series Title}';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
