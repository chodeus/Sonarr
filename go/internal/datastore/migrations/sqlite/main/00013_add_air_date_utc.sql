-- Ports Migration/013_add_air_date_utc.cs.
-- Adds Episodes.AirDateUtc and backfills from AirDate (still DATETIME at v13;
-- migration 014 drops it and 015 re-adds it as TEXT — column-type pivot).
-- See go/analysis/migrations/ledger.md row 13.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "AirDateUtc" DATETIME;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Episodes" SET "AirDateUtc" = "AirDate";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
