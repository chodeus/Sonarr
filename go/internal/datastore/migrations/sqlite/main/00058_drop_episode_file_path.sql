-- Ports Migration/058. EpisodeFiles.Path was UNIQUE inline since v1, so
-- SQLite needs the recreate-table dance (the Postgres counterpart can
-- ALTER DROP COLUMN directly). See ledger row 58.

-- +goose Up
-- +goose StatementBegin

CREATE TABLE "EpisodeFiles_new" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "SeriesId" INTEGER NOT NULL,
    "Quality" TEXT NOT NULL,
    "Size" INTEGER NOT NULL,
    "DateAdded" DATETIME NOT NULL,
    "SeasonNumber" INTEGER NOT NULL,
    "SceneName" TEXT,
    "ReleaseGroup" TEXT,
    "MediaInfo" TEXT,
    "RelativePath" TEXT
);
INSERT INTO "EpisodeFiles_new" SELECT "Id","SeriesId","Quality","Size","DateAdded","SeasonNumber","SceneName","ReleaseGroup","MediaInfo","RelativePath" FROM "EpisodeFiles";
DROP TABLE "EpisodeFiles";
ALTER TABLE "EpisodeFiles_new" RENAME TO "EpisodeFiles";
CREATE INDEX "IX_EpisodeFiles_SeriesId" ON "EpisodeFiles" ("SeriesId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
