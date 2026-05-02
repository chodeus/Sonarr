-- Ports Migration/011_remove_ignored.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 11.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Seasons" DROP COLUMN "Ignored";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes" DROP COLUMN "Ignored";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
