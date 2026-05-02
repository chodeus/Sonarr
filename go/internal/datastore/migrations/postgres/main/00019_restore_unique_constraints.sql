-- Ports Migration/019_restore_unique_constraints.cs for Postgres.
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
SELECT 'rollback not supported; restore from backup' WHERE false;
