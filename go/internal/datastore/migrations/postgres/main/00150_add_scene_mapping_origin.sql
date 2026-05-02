-- Ports Migration/150. See ledger row 150.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "SceneOrigin" text;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "SearchMode" integer;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "Comment" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
