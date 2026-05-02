-- Ports Migration/144. See ledger row 144.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "ImportLists" ADD COLUMN "SeriesType" INTEGER DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ImportLists" ADD COLUMN "SeasonFolder" INTEGER DEFAULT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
