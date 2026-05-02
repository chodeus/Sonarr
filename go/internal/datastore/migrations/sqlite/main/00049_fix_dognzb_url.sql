-- Ports Migration/049. See ledger row 49.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "Settings" = replace("Settings", '//dognzb.cr', '//api.dognzb.cr') WHERE "Implementation" = 'Newznab' AND "Settings" LIKE '%//dognzb.cr%';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
