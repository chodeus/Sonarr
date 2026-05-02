-- Ports Migration/043. Complex C# config-to-DownloadClients backfill —
-- reads Config rows like SabHost / NzbgetHost etc. and creates a
-- DownloadClient row. No-op for fresh DBs; Phase 6 cutover handles
-- legacy data. See ledger row 43.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
