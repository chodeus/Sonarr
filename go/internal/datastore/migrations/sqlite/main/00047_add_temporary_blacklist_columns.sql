-- Ports Migration/047. See ledger row 47.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Blacklist" ADD COLUMN "PublishedDate" DATETIME;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
