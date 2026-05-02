-- Ports Migration/016_updated_imported_history_item.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 16.

-- +goose Up
-- +goose StatementBegin
UPDATE "History" SET "Data" = replace("Data", '"Path"', '"ImportedPath"') WHERE "EventType" = 3;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
