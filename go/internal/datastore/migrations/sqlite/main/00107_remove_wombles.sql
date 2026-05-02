-- Ports Migration/107. See ledger row 107.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Indexers" WHERE "Implementation" = 'Wombles';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
