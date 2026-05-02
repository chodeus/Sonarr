-- Ports Migration/193. See ledger row 193.
-- Two-part migration: creates ImportListItems and adds
-- HasRemovedItemSinceLastClean to ImportListStatus. The latter was
-- missed in the original sweep and added 2026-05-02 to match HEAD.

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
-- +goose StatementBegin
ALTER TABLE "ImportListStatus" ADD COLUMN "HasRemovedItemSinceLastClean" INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
