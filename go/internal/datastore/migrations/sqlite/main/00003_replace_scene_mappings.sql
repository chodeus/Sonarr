-- Ports src/NzbDrone.Core/Datastore/Migration/003_remove_renamed_scene_mapping_columns.cs.
-- Drops the original SceneMappings (Id, CleanTitle, SceneName, TvdbId,
-- SeasonNumber) and recreates it with (Id, TvdbId, SeasonNumber, SearchTerm,
-- ParseTerm). FluentMigrator's drop-and-recreate pattern is preserved here
-- because the C# semantics include "rows are wiped" — both engines do the same.
-- See go/analysis/migrations/ledger.md row 3.

-- +goose Up
-- +goose StatementBegin
DROP TABLE IF EXISTS "SceneMappings";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "SceneMappings" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "TvdbId" INTEGER NOT NULL,
    "SeasonNumber" INTEGER NOT NULL,
    "SearchTerm" TEXT NOT NULL,
    "ParseTerm" TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
