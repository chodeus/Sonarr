-- Ports Migration/156. See ledger row 156.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "DelayProfiles" ADD COLUMN "BypassIfHighestQuality" INTEGER DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "DelayProfiles" SET "BypassIfHighestQuality" = 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
