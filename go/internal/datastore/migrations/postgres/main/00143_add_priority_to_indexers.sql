-- Ports Migration/143. See ledger row 143.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Indexers" ADD COLUMN "Priority" integer NOT NULL DEFAULT 25;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
