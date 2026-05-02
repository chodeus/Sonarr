-- Ports Migration/103. Complex C# extension-fixup logic. No-op for fresh
-- DBs. See ledger row 103.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
