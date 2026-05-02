-- Ports Migration/179. See ledger row 179.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "AutoTagging" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Name" TEXT NOT NULL,
    "Specifications" TEXT NOT NULL DEFAULT '[]',
    "RemoveTagsAutomatically" INTEGER NOT NULL DEFAULT 0,
    "Tags" TEXT NOT NULL DEFAULT '[]'
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_AutoTagging_Name" ON "AutoTagging" ("Name");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
