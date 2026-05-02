-- Ports Migration/053. ADD COLUMN; complex parser-driven SortTitle
-- backfill is no-op (Phase 6 cutover handles legacy DBs). See ledger row 53.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "SortTitle" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
