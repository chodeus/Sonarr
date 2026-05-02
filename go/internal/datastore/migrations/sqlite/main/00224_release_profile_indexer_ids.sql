-- Ports Migration/224. ADD ReleaseProfiles.IndexerIds + DROP IndexerId.
-- Backfill from singular IndexerId is no-op for fresh DBs. See ledger row 224.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "ReleaseProfiles" ADD COLUMN "IndexerIds" TEXT DEFAULT '[]';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ReleaseProfiles" DROP COLUMN "IndexerId";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
