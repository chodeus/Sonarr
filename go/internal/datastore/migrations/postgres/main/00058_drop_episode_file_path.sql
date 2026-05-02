-- Ports Migration/058. EpisodeFiles.Path was UNIQUE inline since v1, so
-- SQLite needs the recreate-table dance (the Postgres counterpart can
-- ALTER DROP COLUMN directly). See ledger row 58.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "EpisodeFiles" DROP COLUMN "Path";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
