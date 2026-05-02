-- Ports Migration/110. Complex C# Config rebalancing. No-op. See ledger row 110.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
