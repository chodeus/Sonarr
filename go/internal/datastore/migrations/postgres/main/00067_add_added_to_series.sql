-- Ports Migration/067. See ledger row 67.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Added" timestamp without time zone;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
