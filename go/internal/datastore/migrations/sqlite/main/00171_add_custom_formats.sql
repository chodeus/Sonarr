-- Ports Migration/171. CREATE CustomFormats + ADD CF columns on QualityProfiles.
-- The PreferredTerms/NamingConfigs backfill is no-op for fresh DBs.
-- See ledger row 171.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "CustomFormats" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Name" TEXT NOT NULL,
    "Specifications" TEXT NOT NULL DEFAULT '[]',
    "IncludeCustomFormatWhenRenaming" INTEGER NOT NULL DEFAULT 0
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_CustomFormats_Name" ON "CustomFormats" ("Name");
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" ADD COLUMN "FormatItems" TEXT NOT NULL DEFAULT '[]';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" ADD COLUMN "MinFormatScore" INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" ADD COLUMN "CutoffFormatScore" INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
