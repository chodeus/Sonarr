-- Ports Migration/198. ADD COLUMNS; complex parser-driven Title backfill no-op.
-- See ledger row 198.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "SubtitleFiles" ADD COLUMN "Title" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "SubtitleFiles" ADD COLUMN "Copy" INTEGER DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
