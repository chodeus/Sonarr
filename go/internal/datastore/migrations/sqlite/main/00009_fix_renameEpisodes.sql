-- Ports src/NzbDrone.Core/Datastore/Migration/009_fix_renameEpisodes.cs.
-- Drops SeasonFolderFormat from NamingConfig and patches up two SQLite-only
-- sentinel values (-1 and -2) that could be left in RenameEpisodes by
-- migration 007 on certain older SQLite builds. The C# version gates these
-- on IfDatabase("sqlite"); the Go port keeps the same gating implicitly
-- by only emitting them in the SQLite migration file. The Postgres
-- counterpart only does the column drop.
-- See go/analysis/migrations/ledger.md row 9.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "NamingConfig" DROP COLUMN "SeasonFolderFormat";
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "NamingConfig" SET "RenameEpisodes" = 1 WHERE "RenameEpisodes" = -1;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "NamingConfig" SET "RenameEpisodes" = 0 WHERE "RenameEpisodes" = -2;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
