-- Ports Migration/0081. Provider-settings JSON shape mutation. No-op
-- for fresh DBs; Phase 6 cutover handles legacy data. See ledger row 81.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
