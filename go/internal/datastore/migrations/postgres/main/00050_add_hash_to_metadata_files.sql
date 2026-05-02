-- Ports Migration/050. See ledger row 50.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "MetadataFiles" ADD COLUMN "Hash" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
