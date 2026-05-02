-- Ports Migration/042. See ledger row 42.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "DownloadClients" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Enable" INTEGER NOT NULL,
    "Name" TEXT NOT NULL,
    "Implementation" TEXT NOT NULL,
    "Settings" TEXT NOT NULL,
    "ConfigContract" TEXT NOT NULL,
    "Protocol" INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
