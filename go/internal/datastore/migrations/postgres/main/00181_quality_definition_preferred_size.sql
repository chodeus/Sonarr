-- Ports Migration/181. See ledger row 181.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "QualityDefinitions" ADD COLUMN "PreferredSize" double precision;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "QualityDefinitions" SET "PreferredSize" = "MaxSize" - 5 WHERE "MaxSize" > 5;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
