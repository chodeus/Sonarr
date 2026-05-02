-- Ports Migration/099. See ledger row 99.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "ExtraFiles" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "SeriesId" integer NOT NULL,
    "SeasonNumber" integer NOT NULL,
    "EpisodeFileId" integer NOT NULL,
    "RelativePath" text NOT NULL,
    "Extension" text NOT NULL,
    "Added" timestamp without time zone NOT NULL,
    "LastUpdated" timestamp without time zone NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "SubtitleFiles" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "SeriesId" integer NOT NULL,
    "SeasonNumber" integer NOT NULL,
    "EpisodeFileId" integer NOT NULL,
    "RelativePath" text NOT NULL,
    "Extension" text NOT NULL,
    "Added" timestamp without time zone NOT NULL,
    "LastUpdated" timestamp without time zone NOT NULL,
    "Language" integer NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
