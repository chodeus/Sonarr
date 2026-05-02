-- Ports Migration/028 for Postgres. Creates the Blacklist table. See ledger row 28.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Blacklist" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "SeriesId" integer NOT NULL,
    "EpisodeIds" text NOT NULL,
    "SourceTitle" text NOT NULL,
    "Quality" text NOT NULL,
    "Date" timestamp without time zone NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
