-- Ports src/NzbDrone.Core/Datastore/Migration/004_updated_history.cs.
-- Drops the original History (NzbTitle, Indexer, NzbInfoUrl, ReleaseGroup
-- columns) and recreates it with the v2 shape (SourceTitle replaces NzbTitle;
-- Data replaces the indexer/group/url trio). Existing rows are dropped.
-- See go/analysis/migrations/ledger.md row 4.

-- +goose Up
-- +goose StatementBegin
DROP TABLE IF EXISTS "History";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "History" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "EpisodeId" INTEGER NOT NULL,
    "SeriesId" INTEGER NOT NULL,
    "SourceTitle" TEXT NOT NULL,
    "Date" DATETIME NOT NULL,
    "Quality" TEXT NOT NULL,
    "Data" TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
