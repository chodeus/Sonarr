-- Ports Migration/182. See ledger row 182.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "DelayProfiles" ADD COLUMN "BypassIfAboveCustomFormatScore" INTEGER DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DelayProfiles" ADD COLUMN "MinimumCustomFormatScore" INTEGER;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
