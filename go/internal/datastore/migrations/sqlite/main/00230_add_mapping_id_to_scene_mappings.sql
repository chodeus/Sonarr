-- Ports Migration/230. See ledger row 230.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "MappingId" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
