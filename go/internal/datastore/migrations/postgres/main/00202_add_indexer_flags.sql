-- Ports Migration/202. See ledger row 202.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Blocklist" ADD COLUMN "IndexerFlags" integer DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "EpisodeFiles" ADD COLUMN "IndexerFlags" integer DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
