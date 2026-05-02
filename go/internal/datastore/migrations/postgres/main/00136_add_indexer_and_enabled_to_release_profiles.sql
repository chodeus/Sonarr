-- Ports Migration/136. See ledger row 136.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "ReleaseProfiles" ADD COLUMN "Enabled" boolean DEFAULT true;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ReleaseProfiles" ADD COLUMN "IndexerId" integer DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
