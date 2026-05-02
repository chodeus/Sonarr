-- Ports Migration/178. See ledger row 178.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "ImportListStatus" DROP COLUMN "LastSyncListInfo";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ImportListStatus" ADD COLUMN "LastInfoSync" DATETIME;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
