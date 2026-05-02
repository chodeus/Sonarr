-- Ports Migration/141 (LogDbUpgrade). See ledger row 141 (log).

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "UpdateHistory" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Date" DATETIME NOT NULL,
    "Version" TEXT NOT NULL,
    "EventType" INTEGER NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_UpdateHistory_Date" ON "UpdateHistory" ("Date");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
