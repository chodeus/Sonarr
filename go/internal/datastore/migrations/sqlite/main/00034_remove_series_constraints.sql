-- Ports Migration/034. The C# migration relaxes three Series constraints:
-- TvRageId is no longer UNIQUE, ImdbId and TitleSlug become nullable.
-- The original sweep punted on the SQLite side and shipped a no-op,
-- claiming SQLite-runtime dedup handled the cases. That left the schema
-- inconsistent with HEAD. Fixed 2026-05-02 with the recreate-table
-- dance — the only way to relax NOT NULL on SQLite.
--
-- At v34 the actual Series shape is 25 columns: the v1 columns minus
-- BacklogSetting (dropped in v8) and CustomStartDate (dropped earlier),
-- plus Year + Seasons added by v20. The recreate lists all 25.
-- See ledger row 34.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Series_new" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "TvdbId" INTEGER NOT NULL UNIQUE,
    "TvRageId" INTEGER NOT NULL,
    "ImdbId" TEXT,
    "Title" TEXT NOT NULL,
    "TitleSlug" TEXT,
    "CleanTitle" TEXT NOT NULL,
    "Status" INTEGER NOT NULL,
    "Overview" TEXT,
    "AirTime" TEXT,
    "Images" TEXT NOT NULL,
    "Path" TEXT NOT NULL,
    "Monitored" INTEGER NOT NULL,
    "QualityProfileId" INTEGER NOT NULL,
    "SeasonFolder" INTEGER NOT NULL,
    "LastInfoSync" DATETIME,
    "LastDiskSync" DATETIME,
    "Runtime" INTEGER NOT NULL,
    "SeriesType" INTEGER NOT NULL,
    "Network" TEXT,
    "UseSceneNumbering" INTEGER NOT NULL,
    "FirstAired" DATETIME,
    "NextAiring" DATETIME,
    "Year" INTEGER,
    "Seasons" TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO "Series_new" SELECT * FROM "Series";
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE "Series";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series_new" RENAME TO "Series";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Series_TvdbId" ON "Series" ("TvdbId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
