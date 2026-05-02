-- Ports Migration/064 (LogDbUpgrade). See ledger row 64 (log-DB column).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Logs" DROP COLUMN "Method";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
