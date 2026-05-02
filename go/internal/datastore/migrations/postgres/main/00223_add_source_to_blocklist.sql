-- Ports Migration/223. See ledger row 223.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Blocklist" ADD COLUMN "Source" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
