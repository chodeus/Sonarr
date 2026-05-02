-- Ports src/NzbDrone.Core/Datastore/Migration/005_added_eventtype_to_history.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 5.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "History" ADD COLUMN "EventType" integer;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
