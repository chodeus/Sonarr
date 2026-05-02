-- Ports Migration/154. See ledger row 154.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "ReleaseProfiles" ADD COLUMN "Name" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
