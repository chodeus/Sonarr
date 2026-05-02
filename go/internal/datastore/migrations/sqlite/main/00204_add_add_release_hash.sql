-- Ports Migration/204. ADD COLUMN; complex backfill no-op. See ledger row 204.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "EpisodeFiles" ADD COLUMN "ReleaseHash" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
