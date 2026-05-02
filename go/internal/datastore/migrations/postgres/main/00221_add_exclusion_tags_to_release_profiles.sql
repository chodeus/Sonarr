-- Ports Migration/221. See ledger row 221.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "ReleaseProfiles" ADD COLUMN "ExcludedTags" text NOT NULL DEFAULT '[]';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
