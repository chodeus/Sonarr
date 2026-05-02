-- Ports Migration/227. See ledger row 227.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "OriginalCountry" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
