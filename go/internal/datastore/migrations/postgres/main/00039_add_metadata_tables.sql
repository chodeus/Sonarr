-- Ports Migration/039 for Postgres. See ledger row 39.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Metadata" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Enable" boolean NOT NULL,
    "Name" text NOT NULL,
    "Implementation" text NOT NULL,
    "Settings" text NOT NULL,
    "ConfigContract" text NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "MetadataFiles" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "SeriesId" integer NOT NULL,
    "Consumer" text NOT NULL,
    "Type" integer NOT NULL,
    "RelativePath" text NOT NULL,
    "LastUpdated" timestamp without time zone NOT NULL,
    "SeasonNumber" integer,
    "EpisodeFileId" integer
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
