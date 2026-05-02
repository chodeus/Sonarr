-- Ports Migration/170. See ledger row 170.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "SubtitleFiles" ADD COLUMN "LanguageTags" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
