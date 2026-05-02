-- Ports Migration/166. HIGH RISK / complex JSON mutation. No-op for
-- fresh DBs; Phase 6 cutover handles legacy data. See ledger row 166.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
