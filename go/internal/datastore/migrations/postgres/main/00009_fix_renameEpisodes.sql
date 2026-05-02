-- Ports src/NzbDrone.Core/Datastore/Migration/009_fix_renameEpisodes.cs for Postgres.
-- The C# version's UPDATE statements are gated on IfDatabase("sqlite") and
-- patch up sentinel values that only occur on SQLite. The Postgres path
-- only drops the SeasonFolderFormat column.
-- See go/analysis/migrations/ledger.md row 9.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "NamingConfig" DROP COLUMN "SeasonFolderFormat";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
