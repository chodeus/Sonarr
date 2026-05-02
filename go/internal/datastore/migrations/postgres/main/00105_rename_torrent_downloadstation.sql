-- Ports Migration/105. See ledger row 105.

-- +goose Up
-- +goose StatementBegin
UPDATE "DownloadClients" SET "Implementation" = 'TorrentDownloadStation' WHERE "Implementation" = 'DownloadStation';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
