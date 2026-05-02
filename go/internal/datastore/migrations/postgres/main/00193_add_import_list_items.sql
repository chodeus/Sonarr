-- Ports Migration/193. See ledger row 193.

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

-- +goose Down
SELECT 'rollback not supported' WHERE false;
