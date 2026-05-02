-- Ports Migration/171. CREATE CustomFormats + ADD CF columns on QualityProfiles.
-- The PreferredTerms/NamingConfigs backfill is no-op for fresh DBs.
-- See ledger row 171.

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

-- +goose Down
SELECT 'rollback not supported' WHERE false;
