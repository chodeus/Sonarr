-- Ports Migration/029. ADD COLUMNS only; the complex C# backfill that
-- composes naming-format strings from individual flag columns is left
-- as a no-op — fresh DBs need no action, and Phase 6 cutover handles
-- legacy C# data migration. See ledger row 29.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "NamingConfig" ADD COLUMN "StandardEpisodeFormat" text;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "NamingConfig" ADD COLUMN "DailyEpisodeFormat" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
