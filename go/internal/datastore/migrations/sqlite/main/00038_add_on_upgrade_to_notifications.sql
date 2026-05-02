-- Ports Migration/038. ADD COLUMN + backfill from OnDownload. See ledger row 38.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "OnUpgrade" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "OnUpgrade" = "OnDownload";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
