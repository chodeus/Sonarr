-- Ports Migration/187. See ledger row 187.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "OnSeriesAdd" boolean DEFAULT false;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
