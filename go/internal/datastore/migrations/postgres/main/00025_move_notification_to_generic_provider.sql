-- Ports Migration/025 for Postgres. See ledger row 25.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "ConfigContract" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
