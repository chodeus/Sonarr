-- Ports Migration/177. See ledger row 177.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "OnManualInteractionRequired" INTEGER DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
