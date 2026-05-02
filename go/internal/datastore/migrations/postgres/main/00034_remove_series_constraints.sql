-- Ports Migration/034 for Postgres. Drops uniqueness on TvRageId, makes
-- ImdbId and TitleSlug nullable. See ledger row 34.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" DROP CONSTRAINT IF EXISTS "Series_TvRageId_key";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ALTER COLUMN "ImdbId" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ALTER COLUMN "TitleSlug" DROP NOT NULL;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
