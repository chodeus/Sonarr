-- Ports Migration/173. See ledger row 173.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Indexers" WHERE "Implementation" = 'Omgwtfnzbs';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
