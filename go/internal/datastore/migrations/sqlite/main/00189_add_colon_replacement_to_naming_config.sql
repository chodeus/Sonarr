-- Ports Migration/189. See ledger row 189.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "NamingConfig" ADD COLUMN "ColonReplacementFormat" INTEGER DEFAULT 4;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
