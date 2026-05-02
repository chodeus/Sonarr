-- Ports Migration/039. Creates Metadata + MetadataFiles tables. See ledger row 39.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Metadata" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Enable" INTEGER NOT NULL,
    "Name" TEXT NOT NULL,
    "Implementation" TEXT NOT NULL,
    "Settings" TEXT NOT NULL,
    "ConfigContract" TEXT NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "MetadataFiles" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "SeriesId" INTEGER NOT NULL,
    "Consumer" TEXT NOT NULL,
    "Type" INTEGER NOT NULL,
    "RelativePath" TEXT NOT NULL,
    "LastUpdated" DATETIME NOT NULL,
    "SeasonNumber" INTEGER,
    "EpisodeFileId" INTEGER
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
