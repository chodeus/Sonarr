-- Ports Migration/013_add_air_date_utc.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 13.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "AirDateUtc" timestamp without time zone;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Episodes" SET "AirDateUtc" = "AirDate";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
