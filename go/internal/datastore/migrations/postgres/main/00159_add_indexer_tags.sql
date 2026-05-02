-- Ports Migration/159. See ledger row 159.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Indexers" ADD COLUMN "Tags" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
