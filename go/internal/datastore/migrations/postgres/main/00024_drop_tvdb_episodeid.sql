-- Ports Migration/024_drop_tvdb_episodeid.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 24.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" DROP COLUMN "TvDbEpisodeId";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
