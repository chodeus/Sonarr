-- Ports src/NzbDrone.Core/Datastore/Migration/008_remove_backlog.cs.
-- Drops BacklogSetting from Series and UseSceneName from NamingConfig.
-- SQLite 3.35+ supports ALTER TABLE DROP COLUMN natively;
-- modernc.org/sqlite v1.50.0 ships SQLite 3.49+, so we use the direct
-- form rather than the rename-recreate dance the C# side may use on
-- older System.Data.SQLite builds.
-- See go/analysis/migrations/ledger.md row 8.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" DROP COLUMN "BacklogSetting";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "NamingConfig" DROP COLUMN "UseSceneName";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
