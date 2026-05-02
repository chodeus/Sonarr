-- Ports Migration/016_updated_imported_history_item.cs.
-- Pure data mutation: rename "Path" key to "ImportedPath" in History.Data
-- JSON for events of type 3 (imported episode).
-- See go/analysis/migrations/ledger.md row 16.

-- +goose Up
-- +goose StatementBegin
UPDATE "History" SET "Data" = replace("Data", '"Path"', '"ImportedPath"') WHERE "EventType" = 3;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
