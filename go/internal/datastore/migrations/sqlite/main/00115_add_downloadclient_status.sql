-- Ports Migration/115. See ledger row 115.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "DownloadClientStatus" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "ProviderId" INTEGER NOT NULL UNIQUE,
    "InitialFailure" DATETIME,
    "MostRecentFailure" DATETIME,
    "EscalationLevel" INTEGER NOT NULL,
    "DisabledTill" DATETIME
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_DownloadClientStatus_ProviderId" ON "DownloadClientStatus" ("ProviderId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
