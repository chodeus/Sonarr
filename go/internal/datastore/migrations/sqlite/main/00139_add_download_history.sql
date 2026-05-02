-- Ports Migration/139. See ledger row 139.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "DownloadHistory" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "EventType" INTEGER NOT NULL,
    "SeriesId" INTEGER NOT NULL,
    "DownloadId" TEXT NOT NULL,
    "SourceTitle" TEXT NOT NULL,
    "Date" DATETIME NOT NULL,
    "Protocol" INTEGER,
    "IndexerId" INTEGER,
    "DownloadClientId" INTEGER,
    "Release" TEXT,
    "Data" TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_DownloadHistory_EventType" ON "DownloadHistory" ("EventType");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_DownloadHistory_SeriesId" ON "DownloadHistory" ("SeriesId");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_DownloadHistory_DownloadId" ON "DownloadHistory" ("DownloadId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
