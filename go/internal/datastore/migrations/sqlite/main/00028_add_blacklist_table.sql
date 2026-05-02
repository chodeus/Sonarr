-- Ports Migration/028. Creates the Blacklist table. See ledger row 28.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Blacklist" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "SeriesId" INTEGER NOT NULL,
    "EpisodeIds" TEXT NOT NULL,
    "SourceTitle" TEXT NOT NULL,
    "Quality" TEXT NOT NULL,
    "Date" DATETIME NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
