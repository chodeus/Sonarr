-- Ports Migration/136. See ledger row 136.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "ReleaseProfiles" ADD COLUMN "Enabled" INTEGER DEFAULT 1;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ReleaseProfiles" ADD COLUMN "IndexerId" INTEGER DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
