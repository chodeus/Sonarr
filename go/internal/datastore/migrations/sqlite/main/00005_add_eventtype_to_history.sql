-- Ports src/NzbDrone.Core/Datastore/Migration/005_added_eventtype_to_history.cs.
-- Adds a nullable EventType column to History. Backfill is left to the C#
-- (and now Go) runtime — no UPDATE in this migration.
-- See go/analysis/migrations/ledger.md row 5.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "History" ADD COLUMN "EventType" INTEGER;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
