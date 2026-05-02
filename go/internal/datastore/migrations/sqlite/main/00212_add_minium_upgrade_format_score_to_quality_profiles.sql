-- Ports Migration/212. See ledger row 212.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" ADD COLUMN "MinUpgradeFormatScore" INTEGER DEFAULT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
