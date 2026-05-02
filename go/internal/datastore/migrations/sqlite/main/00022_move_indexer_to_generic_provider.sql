-- Ports Migration/022_move_indexer_to_generic_provider.cs.
-- See go/analysis/migrations/ledger.md row 22.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Indexers" ADD COLUMN "ConfigContract" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
