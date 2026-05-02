-- Ports Migration/152. See ledger row 152.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "Settings" = Replace("Settings", 'http://api.broadcasthe.net', 'https://api.broadcasthe.net') WHERE "Implementation" = 'BroadcastheNet';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
