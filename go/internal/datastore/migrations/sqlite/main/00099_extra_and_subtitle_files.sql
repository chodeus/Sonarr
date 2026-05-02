-- Ports Migration/099. The C# migration creates ExtraFiles and
-- SubtitleFiles AND adds the Added + Extension columns to MetadataFiles
-- (with a backfill, then ALTER Extension to NOT NULL). The original
-- sweep ported only the two CREATE TABLEs and missed the MetadataFiles
-- alterations entirely. Fixed 2026-05-02.
--
-- Two deliberate deviations from a literal port (both land at the same
-- HEAD shape):
--
--   1. The Extension column on MetadataFiles is declared NOT NULL
--      DEFAULT '' from the start, collapsing C#'s
--      "ADD-nullable + backfill + ALTER-NOT-NULL" pattern. Identical
--      end-state for fresh DBs.
--
--   2. SubtitleFiles.Added is declared NULLABLE here even though C# v99
--      declares it NOT NULL. C# v188 later makes it nullable on
--      Postgres (FluentMigrator's AlterColumn.Nullable()), and the
--      captured HEAD schema shows it nullable on SQLite too. Declaring
--      it nullable from v99 lands at the right HEAD state without
--      requiring the SQLite recreate-table dance in v188. (The broader
--      v188 nullability changes for other tables — Series.LastInfoSync,
--      Episodes.AirDateUtc etc. — are still owed; tracked as follow-up.)
--
-- See ledger row 99.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "ExtraFiles" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "SeriesId" INTEGER NOT NULL,
    "SeasonNumber" INTEGER NOT NULL,
    "EpisodeFileId" INTEGER NOT NULL,
    "RelativePath" TEXT NOT NULL,
    "Extension" TEXT NOT NULL,
    "Added" DATETIME NOT NULL,
    "LastUpdated" DATETIME NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "SubtitleFiles" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "SeriesId" INTEGER NOT NULL,
    "SeasonNumber" INTEGER NOT NULL,
    "EpisodeFileId" INTEGER NOT NULL,
    "RelativePath" TEXT NOT NULL,
    "Extension" TEXT NOT NULL,
    "Added" DATETIME,
    "LastUpdated" DATETIME NOT NULL,
    "Language" INTEGER NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "MetadataFiles" ADD COLUMN "Added" DATETIME;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "MetadataFiles" ADD COLUMN "Extension" TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
