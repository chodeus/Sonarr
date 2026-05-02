-- Ports Migration/210. Complex CustomFilters JSON update. No-op. See ledger row 210.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
