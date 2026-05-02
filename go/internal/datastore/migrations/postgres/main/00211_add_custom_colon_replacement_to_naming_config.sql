-- Ports Migration/211. See ledger row 211.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "NamingConfig" ADD COLUMN "CustomColonReplacementFormat" text NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
