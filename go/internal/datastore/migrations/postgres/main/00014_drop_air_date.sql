-- Ports Migration/014_drop_air_date.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 14.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" DROP COLUMN "AirDate";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
