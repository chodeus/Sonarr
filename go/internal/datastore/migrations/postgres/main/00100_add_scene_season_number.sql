-- Ports Migration/100. ALTER SeasonNumber to nullable on SceneMappings
-- and add the new SceneSeasonNumber column. Postgres supports
-- ALTER COLUMN DROP NOT NULL directly; the SQLite sibling needs the
-- recreate-table dance.
-- See ledger row 100.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ALTER COLUMN "SeasonNumber" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "SceneSeasonNumber" integer;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
