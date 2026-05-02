-- Ports Migration/078. See ledger row 78.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Commands" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Name" TEXT NOT NULL,
    "Body" TEXT NOT NULL,
    "Priority" INTEGER NOT NULL,
    "Status" INTEGER NOT NULL,
    "QueuedAt" DATETIME NOT NULL,
    "StartedAt" DATETIME,
    "EndedAt" DATETIME,
    "Duration" TEXT,
    "Exception" TEXT,
    "Trigger" INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
