-- Ports Migration/126. See ledger row 126.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "CustomFilters" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Type" TEXT NOT NULL,
    "Label" TEXT NOT NULL,
    "Filters" TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
