-- Ports Migration/075. Hardcoded date 2014-01-01. See ledger row 75.

-- +goose Up
-- +goose StatementBegin
UPDATE "ScheduledTasks" SET "LastExecution" = '2014-01-01 00:00:00' WHERE "TypeName" = 'NzbDrone.Core.Tv.Commands.RefreshSeriesCommand';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Series" SET "LastInfoSync" = '2014-01-01 00:00:00';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
