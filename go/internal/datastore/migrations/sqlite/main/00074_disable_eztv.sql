-- Ports Migration/074. See ledger row 74.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "EnableRss" = 0, "EnableSearch" = 0 WHERE "Implementation" = 'Eztv' AND "Settings" LIKE '%ezrss.it%';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
