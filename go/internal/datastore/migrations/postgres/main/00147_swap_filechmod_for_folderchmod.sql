-- Ports Migration/147. SQLite-only complex chmod conversion. No-op for
-- fresh DBs. See ledger row 147.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
