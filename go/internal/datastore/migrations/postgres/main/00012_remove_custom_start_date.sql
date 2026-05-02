-- Ports Migration/012_remove_custom_start_date.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 12.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" DROP COLUMN "CustomStartDate";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
