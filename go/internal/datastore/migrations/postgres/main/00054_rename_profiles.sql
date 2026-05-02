-- Ports Migration/054. RENAME QualityProfiles to Profiles, add columns,
-- create PendingReleases table, add Series.ProfileId. See ledger row 54.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" RENAME TO "Profiles";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Profiles" ADD COLUMN "Language" integer;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Profiles" ADD COLUMN "GrabDelay" integer;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Profiles" ADD COLUMN "GrabDelayMode" integer;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Profiles" SET "Language" = 1, "GrabDelay" = 0, "GrabDelayMode" = 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "ProfileId" integer;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Series" SET "ProfileId" = "QualityProfileId";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "PendingReleases" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "SeriesId" integer NOT NULL,
    "Title" text NOT NULL,
    "Added" timestamp without time zone NOT NULL,
    "ParsedEpisodeInfo" text NOT NULL,
    "Release" text NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
