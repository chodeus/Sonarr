-- Ports Migration/063. See ledger row 63.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "RemotePathMappings" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Host" TEXT NOT NULL,
    "RemotePath" TEXT NOT NULL,
    "LocalPath" TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
