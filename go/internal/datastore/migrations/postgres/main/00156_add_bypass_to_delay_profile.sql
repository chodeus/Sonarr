-- Ports Migration/156. See ledger row 156.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "DelayProfiles" ADD COLUMN "BypassIfHighestQuality" boolean DEFAULT false;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "DelayProfiles" SET "BypassIfHighestQuality" = true;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
