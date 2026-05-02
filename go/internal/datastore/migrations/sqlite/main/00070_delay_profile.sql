-- Ports Migration/070. CREATE DelayProfiles + insert default row. See ledger row 70.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "DelayProfiles" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "EnableUsenet" INTEGER NOT NULL,
    "EnableTorrent" INTEGER NOT NULL,
    "PreferredProtocol" INTEGER NOT NULL,
    "UsenetDelay" INTEGER NOT NULL,
    "TorrentDelay" INTEGER NOT NULL,
    "Order" INTEGER NOT NULL,
    "Tags" TEXT NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO "DelayProfiles" ("EnableUsenet","EnableTorrent","PreferredProtocol","UsenetDelay","TorrentDelay","Order","Tags") VALUES (1, 1, 1, 0, 0, 2147483647, '[]');
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
