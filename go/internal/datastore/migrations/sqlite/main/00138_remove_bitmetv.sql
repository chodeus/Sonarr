-- Ports Migration/138. See ledger row 138.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Indexers" WHERE "Implementation" = 'BitMeTv';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Indexers" SET "EnableRss" = 0, "EnableAutomaticSearch" = 0, "EnableInteractiveSearch" = 0 WHERE "Implementation" = 'Newznab' AND "Settings" LIKE '%usenet-crawler.com%';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
