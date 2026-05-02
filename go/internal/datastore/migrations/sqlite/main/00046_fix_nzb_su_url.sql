-- Ports Migration/046. See ledger row 46.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "Settings" = replace("Settings", '//nzb.su', '//api.nzb.su') WHERE "Implementation" = 'Newznab' AND "Settings" LIKE '%//nzb.su%';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
