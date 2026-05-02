-- Ports Migration/134. See ledger row 134.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "NamingConfig" ADD COLUMN "SpecialsFolderFormat" text;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "NamingConfig" SET "SpecialsFolderFormat" = 'Specials';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
