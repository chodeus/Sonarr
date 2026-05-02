-- Ports Migration/0085. Provider-settings JSON shape mutation. No-op
-- for fresh DBs; Phase 6 cutover handles legacy data. See ledger row 85.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
