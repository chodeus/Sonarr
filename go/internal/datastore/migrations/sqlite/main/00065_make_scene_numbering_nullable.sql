-- Ports Migration/065. See ledger row 65.

-- +goose Up
-- +goose StatementBegin
UPDATE "Episodes" SET "AbsoluteEpisodeNumber" = NULL WHERE "AbsoluteEpisodeNumber" = 0;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Episodes" SET "SceneAbsoluteEpisodeNumber" = NULL WHERE "SceneAbsoluteEpisodeNumber" = 0;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Episodes" SET "SceneSeasonNumber" = NULL, "SceneEpisodeNumber" = NULL WHERE "SceneSeasonNumber" = 0 AND "SceneEpisodeNumber" = 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
