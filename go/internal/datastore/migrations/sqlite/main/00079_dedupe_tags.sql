-- Ports Migration/079. Complex tag-dedup logic + UNIQUE constraint on
-- Label. Fresh DBs have no duplicate tags; we add a unique index instead
-- of trying to retrofit a column constraint. See ledger row 79.

-- +goose Up
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Tags_Label" ON "Tags" ("Label");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
