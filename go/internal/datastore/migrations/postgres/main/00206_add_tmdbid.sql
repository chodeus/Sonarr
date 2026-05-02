-- Ports Migration/206. See ledger row 206.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "TmdbId" integer DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_Series_TmdbId" ON "Series" ("TmdbId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
