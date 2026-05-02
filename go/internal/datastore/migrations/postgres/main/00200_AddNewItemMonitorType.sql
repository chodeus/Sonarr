-- Ports Migration/200. See ledger row 200.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "MonitorNewItems" integer DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ImportLists" ADD COLUMN "MonitorNewItems" integer DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
