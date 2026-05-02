-- Ports Migration/018_remove_duplicates.cs as a no-op for Postgres.
-- See the SQLite counterpart (and analysis/migrations/ledger.md row 18)
-- for the full rationale: the C# version is a one-shot fix for legacy
-- duplicates, which fresh Go DBs can't have because of inline UNIQUE
-- constraints from migration 001.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
