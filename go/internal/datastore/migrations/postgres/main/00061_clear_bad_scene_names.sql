-- Ports Migration/061. See ledger row 61.

-- +goose Up
-- +goose StatementBegin
UPDATE "EpisodeFiles" SET "ReleaseGroup" = NULL, "SceneName" = NULL WHERE "ReleaseGroup" IS NULL OR "SceneName" IS NULL OR "ReleaseGroup" = 'DRONE' OR LENGTH("SceneName") < 10 OR LENGTH("ReleaseGroup") > 20 OR "SceneName" NOT LIKE '%.%';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
