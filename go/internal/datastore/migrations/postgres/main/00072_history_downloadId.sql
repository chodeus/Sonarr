-- Ports Migration/072. ADD COLUMN + CREATE INDEX. The complex JSON
-- key-extraction backfill is no-op for fresh DBs. See ledger row 72.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "History" ADD COLUMN "DownloadId" text;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_History_DownloadId" ON "History" ("DownloadId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
