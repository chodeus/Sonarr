-- Ports Migration/050. See ledger row 50.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "MetadataFiles" ADD COLUMN "Hash" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
