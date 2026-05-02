-- Ports Migration/207. Drop QualityDefinitions size columns; complex
-- Profile size backfill no-op. See ledger row 207.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "QualityDefinitions" DROP COLUMN "MinSize";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityDefinitions" DROP COLUMN "MaxSize";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityDefinitions" DROP COLUMN "PreferredSize";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
