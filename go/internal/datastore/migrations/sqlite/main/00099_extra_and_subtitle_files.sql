-- Ports Migration/099. See ledger row 99.

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
    "Added" DATETIME NOT NULL,
    "LastUpdated" DATETIME NOT NULL,
    "Language" INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
