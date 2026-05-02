-- Ports Migration/174. ADD Salt + Iterations to support PBKDF2 (the
-- pre-174 password column held SHA-256-hex). See ledger row 174.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Users" ADD COLUMN "Salt" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Users" ADD COLUMN "Iterations" INTEGER;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
