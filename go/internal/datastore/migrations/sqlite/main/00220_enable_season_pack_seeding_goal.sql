-- Ports Migration/220. Complex indexer settings JSON. No-op. See ledger row 220.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
