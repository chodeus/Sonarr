-- Ports Migration/182. See ledger row 182.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "DelayProfiles" ADD COLUMN "BypassIfAboveCustomFormatScore" boolean DEFAULT false;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DelayProfiles" ADD COLUMN "MinimumCustomFormatScore" integer;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
