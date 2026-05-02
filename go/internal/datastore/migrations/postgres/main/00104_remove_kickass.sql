-- Ports Migration/104. See ledger row 104.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Indexers" WHERE "Implementation" = 'KickassTorrents';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
