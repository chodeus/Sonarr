-- Ports Migration/171. The C# migration does:
--   * Create.TableForModel("CustomFormats")
--   * Alter.Table("QualityProfiles").AddColumn("FormatItems")
--   * Alter.Table("QualityProfiles").AddColumn("MinFormatScore")
--   * Alter.Table("QualityProfiles").AddColumn("CutoffFormatScore")
--   * Execute.WithConnection (PreferredTerms backfill — no-op for fresh DBs)
--   * Execute.WithConnection (NamingConfigs backfill — no-op for fresh DBs)
--   * Delete.Column("Preferred").FromTable("ReleaseProfiles")
--   * Delete.Column("IncludePreferredWhenRenaming").FromTable("ReleaseProfiles")
--   * Delete.FromTable("ReleaseProfiles").Row(Required='[]', Ignored='[]')
--
-- The original sweep ported only the CREATE TABLE + 3 ADD COLUMN halves
-- and dropped the trailing schema cleanup. Fixed 2026-05-02 — the two
-- DROP COLUMN and the targeted DELETE are added so the schema lands at
-- the same shape as C#.
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
-- +goose StatementBegin
ALTER TABLE "ReleaseProfiles" DROP COLUMN "Preferred";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ReleaseProfiles" DROP COLUMN "IncludePreferredWhenRenaming";
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM "ReleaseProfiles" WHERE "Required" = '[]' AND "Ignored" = '[]';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
