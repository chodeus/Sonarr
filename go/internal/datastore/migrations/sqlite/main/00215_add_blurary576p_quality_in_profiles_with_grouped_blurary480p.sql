-- Ports Migration/215. HIGH RISK Profile group conversion. No-op. See ledger row 215.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
