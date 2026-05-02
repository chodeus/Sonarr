-- Ports Migration/158. ADD COLUMNS; complex Config-driven backfill no-op.
-- See ledger row 158.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "DownloadClients" ADD COLUMN "RemoveCompletedDownloads" INTEGER NOT NULL DEFAULT 1;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DownloadClients" ADD COLUMN "RemoveFailedDownloads" INTEGER NOT NULL DEFAULT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
