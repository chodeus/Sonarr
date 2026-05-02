-- Ports Migration/019_restore_unique_constraints.cs.
-- The C# comment explains: "During an earlier version of drone, the
-- indexes weren't recreated during alter table." The fix DROP IF EXISTS +
-- CREATE UNIQUE INDEX is idempotent on fresh DBs (where the indexes
-- already exist from migration 001).
-- See go/analysis/migrations/ledger.md row 19.

-- +goose Up
-- +goose StatementBegin
DROP INDEX IF EXISTS "IX_Series_TvdbId";
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS "IX_Series_TitleSlug";
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS "IX_Episodes_TvDbEpisodeId";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Series_TvdbId" ON "Series" ("TvdbId");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Series_TitleSlug" ON "Series" ("TitleSlug");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Episodes_TvDbEpisodeId" ON "Episodes" ("TvDbEpisodeId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
