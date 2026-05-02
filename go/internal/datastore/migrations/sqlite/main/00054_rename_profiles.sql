-- Ports Migration/054. RENAME QualityProfiles to Profiles, add columns,
-- create PendingReleases table, add Series.ProfileId. See ledger row 54.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" RENAME TO "Profiles";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Profiles" ADD COLUMN "Language" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Profiles" ADD COLUMN "GrabDelay" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Profiles" ADD COLUMN "GrabDelayMode" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Profiles" SET "Language" = 1, "GrabDelay" = 0, "GrabDelayMode" = 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "ProfileId" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Series" SET "ProfileId" = "QualityProfileId";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "PendingReleases" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "SeriesId" INTEGER NOT NULL,
    "Title" TEXT NOT NULL,
    "Added" DATETIME NOT NULL,
    "ParsedEpisodeInfo" TEXT NOT NULL,
    "Release" TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
