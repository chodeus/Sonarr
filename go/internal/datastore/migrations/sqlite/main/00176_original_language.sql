-- Ports Migration/176. See ledger row 176.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "OriginalLanguage" INTEGER DEFAULT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
