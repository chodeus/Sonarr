-- Ports Migration/128. See ledger row 128.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Profiles" RENAME TO "QualityProfiles";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" ADD COLUMN "UpgradeAllowed" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "LanguageProfiles" ADD COLUMN "UpgradeAllowed" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "QualityProfiles" SET "UpgradeAllowed" = 1;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "LanguageProfiles" SET "UpgradeAllowed" = 1;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" RENAME COLUMN "ProfileId" TO "QualityProfileId";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
