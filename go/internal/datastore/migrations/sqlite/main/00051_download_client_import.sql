-- Ports Migration/051. Complex C# Config + DownloadClients backfill —
-- enables completed-download-handling, converts FolderSettings, associates
-- imported History rows with DownloadClients. No-op for fresh DBs; Phase 6
-- cutover handles legacy data. See ledger row 51.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
