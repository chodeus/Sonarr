-- Ports Migration/191. See ledger row 191.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "DownloadClients" ADD COLUMN "Tags" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
