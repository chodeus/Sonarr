-- Ports Migration/186. See ledger row 186.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Commands" ADD COLUMN "Result" integer DEFAULT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
