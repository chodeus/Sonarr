-- Ports Migration/119. The C# migration does FOUR things: rename
-- EnableSearch → EnableAutomaticSearch, ADD nullable EnableInteractiveSearch,
-- UPDATE to copy values, then ALTER EnableInteractiveSearch to NOT NULL.
-- The original sweep dropped the final NOT NULL constraint; the column
-- ended up nullable instead of NOT NULL. Fixed 2026-05-02.
--
-- The fix collapses ADD-nullable + UPDATE + ALTER-NOT-NULL into a single
-- ADD-NOT-NULL-WITH-DEFAULT + UPDATE. The DEFAULT 0 is a placeholder
-- existing rows briefly hold before the UPDATE rewrites every row from
-- EnableAutomaticSearch. End state matches C#: EnableInteractiveSearch
-- is NOT NULL after this migration. SQLite has no ALTER COLUMN, so this
-- collapse avoids the recreate-table dance entirely.
-- See ledger row 119.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Indexers" RENAME COLUMN "EnableSearch" TO "EnableAutomaticSearch";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Indexers" ADD COLUMN "EnableInteractiveSearch" INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Indexers" SET "EnableInteractiveSearch" = "EnableAutomaticSearch";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
