-- Ports Migration/096. See ledger row 96.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "EnableRss" = false, "EnableSearch" = false, "Settings" = Replace("Settings", 'https://kat.cr', '') WHERE "Implementation" = 'KickassTorrents' AND "Settings" LIKE '%kat.cr%';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
