-- Ports Migration/180. See ledger row 180.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "ScheduledTasks" ADD COLUMN "LastStartTime" DATETIME;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
