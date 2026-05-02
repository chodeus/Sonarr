-- Ports Migration/196. See ledger row 196.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "FinaleType" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
