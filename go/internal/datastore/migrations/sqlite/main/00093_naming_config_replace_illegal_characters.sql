-- Ports Migration/093. See ledger row 93.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "NamingConfig" ADD COLUMN "ReplaceIllegalCharacters" INTEGER DEFAULT 1;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "NamingConfig" SET "ReplaceIllegalCharacters" = 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
