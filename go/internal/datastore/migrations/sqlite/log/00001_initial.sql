-- Ports the LogDbUpgrade() half of src/NzbDrone.Core/Datastore/Migration/001_initial_setup.cs.
-- This is the initial Logs table. Migration 006 will drop and recreate it
-- with the Time column indexed.
-- See go/analysis/migrations/ledger.md row 1 (log-DB column).

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Logs" (
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

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
