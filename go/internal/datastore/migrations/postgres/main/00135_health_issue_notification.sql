-- Ports Migration/135. See ledger row 135.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "OnHealthIssue" boolean DEFAULT false;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "IncludeHealthWarnings" boolean DEFAULT false;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
