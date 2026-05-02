-- Ports Migration/142. CREATE ImportLists + ImportListStatus + ImportListExclusions.
-- See ledger row 142.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "ImportLists" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Name" TEXT NOT NULL,
    "Implementation" TEXT NOT NULL,
    "Settings" TEXT,
    "ConfigContract" TEXT,
    "EnableAutomaticAdd" INTEGER,
    "RootFolderPath" TEXT NOT NULL,
    "ShouldMonitor" INTEGER NOT NULL,
    "QualityProfileId" INTEGER NOT NULL,
    "LanguageProfileId" INTEGER NOT NULL,
    "Tags" TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_ImportLists_Name" ON "ImportLists" ("Name");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "ImportListStatus" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "ProviderId" INTEGER NOT NULL UNIQUE,
    "InitialFailure" DATETIME,
    "MostRecentFailure" DATETIME,
    "EscalationLevel" INTEGER NOT NULL,
    "DisabledTill" DATETIME,
    "LastSyncListInfo" TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_ImportListStatus_ProviderId" ON "ImportListStatus" ("ProviderId");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "ImportListExclusions" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "TvdbId" TEXT NOT NULL UNIQUE,
    "Title" TEXT NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_ImportListExclusions_TvdbId" ON "ImportListExclusions" ("TvdbId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
