-- Ports Migration/015_add_air_date_as_string.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 15.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "AirDate" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
