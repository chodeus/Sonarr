-- Ports Migration/059. See ledger row 59.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Indexers" ADD COLUMN "EnableRss" boolean;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Indexers" ADD COLUMN "EnableSearch" boolean;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Indexers" SET "EnableRss" = "Enable", "EnableSearch" = "Enable";
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Indexers" SET "EnableSearch" = false WHERE "Implementation" = 'Wombles';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
