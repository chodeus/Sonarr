-- Ports Migration/111. CREATE LanguageProfiles + ADD Series.LanguageProfileId,
-- DROP Profiles.Language. The default-profile-insertion backfill is no-op
-- (Profiles is empty on fresh DBs). See ledger row 111.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "LanguageProfiles" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Name" text NOT NULL,
    "Languages" text NOT NULL,
    "Cutoff" integer NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_LanguageProfiles_Name" ON "LanguageProfiles" ("Name");
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "LanguageProfileId" integer DEFAULT 1;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Profiles" DROP COLUMN "Language";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
