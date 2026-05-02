-- Ports Migration/091. See ledger row 91.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "IndexerStatus" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "IndexerId" INTEGER NOT NULL UNIQUE,
    "InitialFailure" DATETIME,
    "MostRecentFailure" DATETIME,
    "EscalationLevel" INTEGER NOT NULL,
    "DisabledTill" DATETIME,
    "LastRssSyncReleaseInfo" TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_IndexerStatus_IndexerId" ON "IndexerStatus" ("IndexerId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
