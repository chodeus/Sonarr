-- Ports Migration/165. See ledger row 165.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "OnApplicationUpdate" INTEGER DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
