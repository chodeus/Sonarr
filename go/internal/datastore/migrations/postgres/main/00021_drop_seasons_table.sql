-- Ports Migration/021_drop_seasons_table.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 21.

-- +goose Up
-- +goose StatementBegin
DROP TABLE "Seasons";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
