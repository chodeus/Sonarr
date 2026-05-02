-- Ports Migration/101. HIGH RISK / complex JSON mutation. No-op for
-- fresh DBs; Phase 6 cutover handles legacy data. See ledger row 101.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
