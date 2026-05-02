-- Ports src/NzbDrone.Core/Datastore/Migration/010_add_monitored.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 10.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "Monitored" boolean;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Seasons" ADD COLUMN "Monitored" boolean;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Episodes" SET "Monitored" = true WHERE "Ignored" = false;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Episodes" SET "Monitored" = false WHERE "Ignored" = true;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Seasons" SET "Monitored" = true WHERE "Ignored" = false;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Seasons" SET "Monitored" = false WHERE "Ignored" = true;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
