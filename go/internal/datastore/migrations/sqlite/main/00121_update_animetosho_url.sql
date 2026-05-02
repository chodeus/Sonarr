-- Ports Migration/121. See ledger row 121.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "Settings" = Replace(Replace("Settings", '//animetosho.org', '//feed.animetosho.org'), '/feed/nabapi', '/nabapi') WHERE ("Implementation" = 'Newznab' OR "Implementation" = 'Torznab') AND "Settings" LIKE '%animetosho%';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
