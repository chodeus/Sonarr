-- Ports Migration/090. See ledger row 90.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "Settings" = Replace("Settings", 'kickass.so', 'kat.cr') WHERE "Implementation" = 'KickassTorrents';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Indexers" SET "Settings" = Replace("Settings", 'kickass.to', 'kat.cr') WHERE "Implementation" = 'KickassTorrents';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Indexers" SET "Settings" = Replace("Settings", 'http://', 'https://') WHERE "Implementation" = 'KickassTorrents';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
