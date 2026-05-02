-- Ports src/NzbDrone.Core/Datastore/Migration/002_remove_tvrage_imdb_unique_constraint.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 2.

-- +goose Up
-- +goose StatementBegin
DROP INDEX IF EXISTS "IX_Series_TvRageId";
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS "IX_Series_ImdbId";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
