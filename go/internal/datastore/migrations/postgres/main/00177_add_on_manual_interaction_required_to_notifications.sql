-- Ports Migration/177. See ledger row 177.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "OnManualInteractionRequired" boolean DEFAULT false;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
