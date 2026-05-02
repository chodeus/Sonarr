-- Ports Migration/146 (LogDbUpgrade). Complex C# dedup on UpdateHistory.
-- No-op for fresh log DBs. See ledger row 146 (log).

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
