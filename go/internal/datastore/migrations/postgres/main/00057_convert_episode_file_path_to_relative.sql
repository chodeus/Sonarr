-- Ports Migration/057. ADD COLUMN; complex backfill (REPLACE Path with
-- RelativePath) is no-op for fresh DBs. See ledger row 57.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "EpisodeFiles" ADD COLUMN "RelativePath" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
