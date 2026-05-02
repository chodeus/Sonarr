-- Ports Migration/109. Complex C# Config-driven backfill. No-op. See ledger row 109.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
