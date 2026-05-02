-- Ports Migration/203. ADD COLUMN; complex backfill no-op. See ledger row 203.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Blocklist" ADD COLUMN "ReleaseType" INTEGER DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "EpisodeFiles" ADD COLUMN "ReleaseType" INTEGER DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
