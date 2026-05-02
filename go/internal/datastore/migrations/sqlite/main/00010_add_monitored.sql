-- Ports src/NzbDrone.Core/Datastore/Migration/010_add_monitored.cs.
-- Adds nullable Monitored to Episodes and Seasons; backfills it as the
-- inverse of the legacy Ignored column. The Ignored column is left in
-- place by this migration — a later migration removes it (cross-cite
-- 011_remove_ignored).
-- See go/analysis/migrations/ledger.md row 10.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "Monitored" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Seasons" ADD COLUMN "Monitored" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Episodes" SET "Monitored" = 1 WHERE "Ignored" = 0;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Episodes" SET "Monitored" = 0 WHERE "Ignored" = 1;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Seasons" SET "Monitored" = 1 WHERE "Ignored" = 0;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Seasons" SET "Monitored" = 0 WHERE "Ignored" = 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
