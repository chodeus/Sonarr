-- Ports Migration/114. RENAME column IndexerStatus.IndexerId → ProviderId.
-- See ledger row 114.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "IndexerStatus" RENAME COLUMN "IndexerId" TO "ProviderId";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
