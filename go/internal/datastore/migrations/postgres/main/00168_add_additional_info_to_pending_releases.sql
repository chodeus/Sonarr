-- Ports Migration/168. See ledger row 168.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "PendingReleases" ADD COLUMN "AdditionalInfo" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
