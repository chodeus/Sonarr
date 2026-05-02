-- Ports Migration/096. See ledger row 96.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "EnableRss" = 0, "EnableSearch" = 0, "Settings" = Replace("Settings", 'https://kat.cr', '') WHERE "Implementation" = 'KickassTorrents' AND "Settings" LIKE '%kat.cr%';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
