-- Ports src/NzbDrone.Core/Datastore/Migration/008_remove_backlog.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 8.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" DROP COLUMN "BacklogSetting";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "NamingConfig" DROP COLUMN "UseSceneName";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
