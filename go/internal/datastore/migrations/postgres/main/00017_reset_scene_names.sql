-- Ports Migration/017_reset_scene_names.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 17.

-- +goose Up
-- +goose StatementBegin
UPDATE "EpisodeFiles" SET "SceneName" = NULL WHERE "SceneName" != NULL;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
