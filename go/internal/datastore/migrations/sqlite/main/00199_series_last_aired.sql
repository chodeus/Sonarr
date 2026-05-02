-- Ports Migration/199. See ledger row 199.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "LastAired" DATETIME;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
