-- Ports Migration/048. See ledger row 48.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "Title" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
