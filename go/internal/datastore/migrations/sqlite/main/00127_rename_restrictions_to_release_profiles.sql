-- Ports Migration/127. See ledger row 127.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Restrictions" RENAME TO "ReleaseProfiles";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ReleaseProfiles" ADD COLUMN "IncludePreferredWhenRenaming" INTEGER DEFAULT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
