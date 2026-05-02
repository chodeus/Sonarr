-- Ports Migration/119. See ledger row 119.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Indexers" RENAME COLUMN "EnableSearch" TO "EnableAutomaticSearch";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Indexers" ADD COLUMN "EnableInteractiveSearch" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Indexers" SET "EnableInteractiveSearch" = "EnableAutomaticSearch";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
