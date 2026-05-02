-- Ports src/NzbDrone.Core/Datastore/Migration/002_remove_tvrage_imdb_unique_constraint.cs.
-- Drops the IX_Series_TvRageId and IX_Series_ImdbId indexes (the inline
-- column UNIQUE constraints on Series.TvRageId/ImdbId are kept by FluentMigrator's
-- semantics — only the explicit IX_* indexes are deleted here).
-- See go/analysis/migrations/ledger.md row 2.

-- +goose Up
-- +goose StatementBegin
DROP INDEX IF EXISTS "IX_Series_TvRageId";
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS "IX_Series_ImdbId";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
