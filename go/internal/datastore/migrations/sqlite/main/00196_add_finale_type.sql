-- Ports Migration/196. See ledger row 196.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "FinaleType" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
