-- Ports Migration/194. See ledger row 194.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "NotificationStatus" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "ProviderId" INTEGER NOT NULL UNIQUE,
    "InitialFailure" DATETIME,
    "MostRecentFailure" DATETIME,
    "EscalationLevel" INTEGER NOT NULL,
    "DisabledTill" DATETIME
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_NotificationStatus_ProviderId" ON "NotificationStatus" ("ProviderId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
