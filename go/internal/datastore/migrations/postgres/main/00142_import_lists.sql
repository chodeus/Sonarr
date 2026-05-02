-- Ports Migration/142. CREATE ImportLists + ImportListStatus + ImportListExclusions.
-- See ledger row 142.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "ImportLists" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Name" text NOT NULL,
    "Implementation" text NOT NULL,
    "Settings" text,
    "ConfigContract" text,
    "EnableAutomaticAdd" boolean,
    "RootFolderPath" text NOT NULL,
    "ShouldMonitor" integer NOT NULL,
    "QualityProfileId" integer NOT NULL,
    "LanguageProfileId" integer NOT NULL,
    "Tags" text
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_ImportLists_Name" ON "ImportLists" ("Name");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "ImportListStatus" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "ProviderId" integer NOT NULL UNIQUE,
    "InitialFailure" timestamp without time zone,
    "MostRecentFailure" timestamp without time zone,
    "EscalationLevel" integer NOT NULL,
    "DisabledTill" timestamp without time zone,
    "LastSyncListInfo" text
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_ImportListStatus_ProviderId" ON "ImportListStatus" ("ProviderId");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "ImportListExclusions" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "TvdbId" text NOT NULL UNIQUE,
    "Title" text NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_ImportListExclusions_TvdbId" ON "ImportListExclusions" ("TvdbId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
