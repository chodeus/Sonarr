-- Ports Migration/144. See ledger row 144.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "ImportLists" ADD COLUMN "SeriesType" integer DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ImportLists" ADD COLUMN "SeasonFolder" boolean DEFAULT true;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
