-- Ports Migration/097. See ledger row 97.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "PendingReleases" ADD COLUMN "Reason" INTEGER DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
