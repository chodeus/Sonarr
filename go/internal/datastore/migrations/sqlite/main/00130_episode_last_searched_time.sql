-- Ports Migration/130. See ledger row 130.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "LastSearchTime" DATETIME;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
