-- Ports Migration/099. See SQLite sibling for the full discussion of
-- the deliberate deviations (collapsed MetadataFiles.Extension nullable+
-- ALTER-NOT-NULL into a single NOT NULL DEFAULT, and SubtitleFiles.Added
-- declared nullable from v99 instead of waiting for v188's per-engine
-- nullability changes).
-- See ledger row 99.

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
    "Added" timestamp without time zone,
    "LastUpdated" timestamp without time zone NOT NULL,
    "Language" integer NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "MetadataFiles" ADD COLUMN "Added" timestamp without time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "MetadataFiles" ADD COLUMN "Extension" text NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
