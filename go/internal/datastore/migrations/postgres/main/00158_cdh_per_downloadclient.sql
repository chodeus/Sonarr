-- Ports Migration/158. ADD COLUMNS; complex Config-driven backfill no-op.
-- See ledger row 158.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "DownloadClients" ADD COLUMN "RemoveCompletedDownloads" boolean NOT NULL DEFAULT true;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DownloadClients" ADD COLUMN "RemoveFailedDownloads" boolean NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
