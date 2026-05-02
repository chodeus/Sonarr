-- Ports Migration/011_remove_ignored.cs.
-- Drops the legacy Ignored column from Seasons and Episodes (replaced by
-- Monitored in migration 010, which backfilled from this column).
-- See go/analysis/migrations/ledger.md row 11.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Seasons" DROP COLUMN "Ignored";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes" DROP COLUMN "Ignored";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
