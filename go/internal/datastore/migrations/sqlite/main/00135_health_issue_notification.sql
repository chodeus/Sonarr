-- Ports Migration/135. See ledger row 135.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "OnHealthIssue" INTEGER DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "IncludeHealthWarnings" INTEGER DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
