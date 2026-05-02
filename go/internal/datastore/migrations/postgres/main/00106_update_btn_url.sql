-- Ports Migration/106. See ledger row 106.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "Settings" = Replace("Settings", 'api.btnapps.net', 'api.broadcasthe.net') WHERE "Implementation" = 'BroadcastheNet';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
