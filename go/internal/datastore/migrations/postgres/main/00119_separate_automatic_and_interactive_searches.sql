-- Ports Migration/119. The C# migration does FOUR things: rename
-- EnableSearch → EnableAutomaticSearch, ADD nullable EnableInteractiveSearch,
-- UPDATE to copy values, then ALTER EnableInteractiveSearch to NOT NULL.
-- The original sweep dropped the final NOT NULL constraint; fixed
-- 2026-05-02.
--
-- Postgres could ALTER COLUMN SET NOT NULL after the UPDATE, but
-- collapsing into ADD-NOT-NULL-WITH-DEFAULT + UPDATE matches the
-- SQLite sibling and produces the same end state.
-- See ledger row 119.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Indexers" RENAME COLUMN "EnableSearch" TO "EnableAutomaticSearch";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Indexers" ADD COLUMN "EnableInteractiveSearch" boolean NOT NULL DEFAULT false;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Indexers" SET "EnableInteractiveSearch" = "EnableAutomaticSearch";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
