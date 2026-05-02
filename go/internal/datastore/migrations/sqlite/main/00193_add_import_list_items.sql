-- Ports Migration/193. See ledger row 193.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "ImportListItems" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "ImportListId" INTEGER NOT NULL,
    "Title" TEXT NOT NULL,
    "TvdbId" INTEGER NOT NULL,
    "Year" INTEGER,
    "TmdbId" INTEGER,
    "ImdbId" TEXT,
    "MalId" INTEGER,
    "AniListId" INTEGER,
    "ReleaseDate" DATETIME
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
