-- Ports Migration/052. See ledger row 52.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "Type" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM "SceneMappings";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "NamingConfig" ADD COLUMN "AnimeEpisodeFormat" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "NamingConfig" SET "AnimeEpisodeFormat" = "StandardEpisodeFormat";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
