-- Ports src/NzbDrone.Core/Datastore/Migration/004_updated_history.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 4.

-- +goose Up
-- +goose StatementBegin
DROP TABLE IF EXISTS "History";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "History" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "EpisodeId" integer NOT NULL,
    "SeriesId" integer NOT NULL,
    "SourceTitle" text NOT NULL,
    "Date" timestamp without time zone NOT NULL,
    "Quality" text NOT NULL,
    "Data" text NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
