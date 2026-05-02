-- Ports Migration/017_reset_scene_names.cs.
-- Resets EpisodeFiles.SceneName to NULL — earlier versions of Sonarr stored
-- the imported file name as scene name, which was wrong.
-- The C# WHERE clause is "SceneName != NULL" which is always false in SQL
-- (NULL comparisons need IS NOT NULL); preserved verbatim for fidelity.
-- See go/analysis/migrations/ledger.md row 17.

-- +goose Up
-- +goose StatementBegin
UPDATE "EpisodeFiles" SET "SceneName" = NULL WHERE "SceneName" != NULL;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
