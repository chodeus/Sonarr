-- Ports Migration/116. See ledger row 116.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "EnableRss" = 0, "EnableSearch" = 0, "Settings" = Replace("Settings", 'https://nyaa.se', '') WHERE "Implementation" = 'Nyaa' AND "Settings" LIKE '%nyaa.se%';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
