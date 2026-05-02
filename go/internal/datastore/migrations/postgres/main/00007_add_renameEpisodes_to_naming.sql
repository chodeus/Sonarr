-- Ports src/NzbDrone.Core/Datastore/Migration/007_add_renameEpisodes_to_naming.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 7.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "NamingConfig" ADD COLUMN "RenameEpisodes" boolean;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "NamingConfig" SET "RenameEpisodes" = NOT "UseSceneName";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
