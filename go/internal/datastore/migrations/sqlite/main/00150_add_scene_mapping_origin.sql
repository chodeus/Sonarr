-- Ports Migration/150. See ledger row 150.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "SceneOrigin" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "SearchMode" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "Comment" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
