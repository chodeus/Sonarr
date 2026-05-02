-- Ports Migration/015_add_air_date_as_string.cs.
-- Re-adds AirDate to Episodes, this time as TEXT instead of DATETIME.
-- The C# side stores air dates as strings to handle TZ edge cases.
-- See go/analysis/migrations/ledger.md row 15.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "AirDate" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
