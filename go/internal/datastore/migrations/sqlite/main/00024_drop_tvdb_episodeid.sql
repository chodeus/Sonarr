-- Ports Migration/024_drop_tvdb_episodeid.cs.
-- TvDbEpisodeId was declared INTEGER NOT NULL UNIQUE inline in migration
-- 001. modernc/sqlite refuses ALTER TABLE DROP COLUMN on a UNIQUE column,
-- so we use the SQLite recreate-table dance: build a new table with the
-- column omitted, copy the rows, drop the old, rename.
-- See go/analysis/migrations/ledger.md row 24.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Episodes_new" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "SeriesId" INTEGER NOT NULL,
    "SeasonNumber" INTEGER NOT NULL,
    "EpisodeNumber" INTEGER NOT NULL,
    "Title" TEXT,
    "Overview" TEXT,
    "EpisodeFileId" INTEGER,
    "AbsoluteEpisodeNumber" INTEGER,
    "SceneAbsoluteEpisodeNumber" INTEGER,
    "SceneSeasonNumber" INTEGER,
    "SceneEpisodeNumber" INTEGER,
    "Monitored" INTEGER,
    "AirDateUtc" DATETIME,
    "AirDate" TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO "Episodes_new" ("Id","SeriesId","SeasonNumber","EpisodeNumber","Title","Overview","EpisodeFileId","AbsoluteEpisodeNumber","SceneAbsoluteEpisodeNumber","SceneSeasonNumber","SceneEpisodeNumber","Monitored","AirDateUtc","AirDate")
SELECT "Id","SeriesId","SeasonNumber","EpisodeNumber","Title","Overview","EpisodeFileId","AbsoluteEpisodeNumber","SceneAbsoluteEpisodeNumber","SceneSeasonNumber","SceneEpisodeNumber","Monitored","AirDateUtc","AirDate" FROM "Episodes";
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE "Episodes";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes_new" RENAME TO "Episodes";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
