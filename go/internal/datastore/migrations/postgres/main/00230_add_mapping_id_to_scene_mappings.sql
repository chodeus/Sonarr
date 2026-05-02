-- Ports Migration/230. See ledger row 230.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "MappingId" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
