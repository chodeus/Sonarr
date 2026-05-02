-- Ports src/NzbDrone.Core/Datastore/Migration/007_add_renameEpisodes_to_naming.cs.
-- Adds a nullable RenameEpisodes column to NamingConfig and backfills it
-- as the inverse of UseSceneName. Existing rows get a value; new rows
-- inherit the column default (NULL).
-- See go/analysis/migrations/ledger.md row 7.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "NamingConfig" ADD COLUMN "RenameEpisodes" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "NamingConfig" SET "RenameEpisodes" = NOT "UseSceneName";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
