-- Ports Migration/022_move_indexer_to_generic_provider.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 22.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Indexers" ADD COLUMN "ConfigContract" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
