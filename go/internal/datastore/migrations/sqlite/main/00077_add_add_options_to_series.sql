-- Ports Migration/077. See ledger row 77.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "AddOptions" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
