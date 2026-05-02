-- Ports Migration/128. See ledger row 128.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Profiles" RENAME TO "QualityProfiles";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" ADD COLUMN "UpgradeAllowed" boolean;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "LanguageProfiles" ADD COLUMN "UpgradeAllowed" boolean;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "QualityProfiles" SET "UpgradeAllowed" = true;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "LanguageProfiles" SET "UpgradeAllowed" = true;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" RENAME COLUMN "ProfileId" TO "QualityProfileId";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
