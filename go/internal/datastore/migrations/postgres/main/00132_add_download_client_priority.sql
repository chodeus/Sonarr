-- Ports Migration/132. ADD Priority with default 1; complex per-protocol
-- backfill is no-op for fresh DBs. See ledger row 132.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "DownloadClients" ADD COLUMN "Priority" integer DEFAULT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
