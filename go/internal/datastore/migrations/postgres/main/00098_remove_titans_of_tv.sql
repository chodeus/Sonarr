-- Ports Migration/098. See ledger row 98.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Indexers" WHERE "Implementation" = 'TitansOfTv';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
