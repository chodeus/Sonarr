-- Ports Migration/219. See ledger row 219.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "Settings" = replace("Settings", '//api.nzb.su', '//api.nzb.life') WHERE "Implementation" = 'Newznab' AND "Settings" LIKE '%//api.nzb.su%';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
