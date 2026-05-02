-- Ports src/NzbDrone.Core/Datastore/Migration/003_remove_renamed_scene_mapping_columns.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 3.

-- +goose Up
-- +goose StatementBegin
DROP TABLE IF EXISTS "SceneMappings";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "SceneMappings" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "TvdbId" integer NOT NULL,
    "SeasonNumber" integer NOT NULL,
    "SearchTerm" text NOT NULL,
    "ParseTerm" text NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
