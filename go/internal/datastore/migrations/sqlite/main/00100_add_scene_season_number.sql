-- Ports Migration/100. ALTER SeasonNumber to nullable on SceneMappings;
-- on SQLite the existing column remains nullable (no constraint).
-- See ledger row 100.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "SceneSeasonNumber" INTEGER;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
