-- Ports Migration/179. See ledger row 179.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "AutoTagging" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Name" text NOT NULL,
    "Specifications" text NOT NULL DEFAULT '[]',
    "RemoveTagsAutomatically" boolean NOT NULL DEFAULT false,
    "Tags" text NOT NULL DEFAULT '[]'
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_AutoTagging_Name" ON "AutoTagging" ("Name");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
