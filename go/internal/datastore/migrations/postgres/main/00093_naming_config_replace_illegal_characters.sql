-- Ports Migration/093. See ledger row 93.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "NamingConfig" ADD COLUMN "ReplaceIllegalCharacters" boolean DEFAULT true;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "NamingConfig" SET "ReplaceIllegalCharacters" = true;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
