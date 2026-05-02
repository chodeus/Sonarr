-- Ports Migration/193. See ledger row 193.
-- Two-part migration: creates ImportListItems and adds
-- HasRemovedItemSinceLastClean to ImportListStatus. The latter was
-- missed in the original sweep and added 2026-05-02 to match HEAD.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "ImportListItems" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "ImportListId" integer NOT NULL,
    "Title" text NOT NULL,
    "TvdbId" integer NOT NULL,
    "Year" integer,
    "TmdbId" integer,
    "ImdbId" text,
    "MalId" integer,
    "AniListId" integer,
    "ReleaseDate" timestamp without time zone
);
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ImportListStatus" ADD COLUMN "HasRemovedItemSinceLastClean" boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
