-- Ports Migration/087. See ledger row 87.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Indexers" WHERE "Implementation" = 'Eztv';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
