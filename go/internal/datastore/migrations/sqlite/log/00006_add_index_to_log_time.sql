-- Ports src/NzbDrone.Core/Datastore/Migration/006_add_index_to_log_time.cs (LogDbUpgrade).
-- Drops the original Logs table and recreates it with the same columns
-- but with the Time column indexed. The C# side uses FluentMigrator's
-- .Indexed() shorthand which emits an "IX_Logs_Time" index name.
-- Existing log rows are dropped (intentional — log noise, not user data).
-- See go/analysis/migrations/ledger.md row 6 (log-DB column).

-- +goose Up
-- +goose StatementBegin
DROP TABLE IF EXISTS "Logs";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "Logs" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Message" TEXT NOT NULL,
    "Time" DATETIME NOT NULL,
    "Logger" TEXT NOT NULL,
    "Method" TEXT,
    "Exception" TEXT,
    "ExceptionType" TEXT,
    "Level" TEXT NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_Logs_Time" ON "Logs" ("Time" ASC);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
