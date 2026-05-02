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
-- The original sweep dropped the trailing schema cleanup. Fixed
-- 2026-05-02. See ledger row 171.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "CustomFormats" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Name" text NOT NULL,
    "Specifications" text NOT NULL DEFAULT '[]',
    "IncludeCustomFormatWhenRenaming" boolean NOT NULL DEFAULT false
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_CustomFormats_Name" ON "CustomFormats" ("Name");
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" ADD COLUMN "FormatItems" text NOT NULL DEFAULT '[]';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" ADD COLUMN "MinFormatScore" integer NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" ADD COLUMN "CutoffFormatScore" integer NOT NULL DEFAULT 0;
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
SELECT 'rollback not supported' WHERE false;
