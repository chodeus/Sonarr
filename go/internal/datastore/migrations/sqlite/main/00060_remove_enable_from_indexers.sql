-- Ports Migration/060. See ledger row 60.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Indexers" DROP COLUMN "Enable";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DownloadClients" DROP COLUMN "Protocol";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
