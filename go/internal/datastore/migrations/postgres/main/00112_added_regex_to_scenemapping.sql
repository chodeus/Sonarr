-- Ports Migration/112. See ledger row 112.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "FilterRegex" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
