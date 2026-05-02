-- Ports Migration/175. HIGH RISK — wrap singleton Language column into
-- Languages JSON array on EpisodeFiles, History, Blocklist (cumulativity-
-- sensitive UPDATE), then drop the now-redundant Language columns plus
-- the LanguageProfiles table and its references on Series + ImportLists.
--
-- The original sweep ported the ADD COLUMN + UPDATE half but dropped the
-- DROP COLUMN + DROP TABLE trailing cleanup. Fixed 2026-05-02.
-- See ledger row 175.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "EpisodeFiles" ADD COLUMN "Languages" text NOT NULL DEFAULT '[]';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "History" ADD COLUMN "Languages" text NOT NULL DEFAULT '[]';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blocklist" ADD COLUMN "Languages" text NOT NULL DEFAULT '[]';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "EpisodeFiles" SET "Languages" = '[' || "Language" || ']';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "History" SET "Languages" = '[' || "Language" || ']';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Blocklist" SET "Languages" = '[' || "Language" || ']';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "EpisodeFiles" DROP COLUMN "Language";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "History" DROP COLUMN "Language";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blocklist" DROP COLUMN "Language";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" DROP COLUMN "LanguageProfileId";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ImportLists" DROP COLUMN "LanguageProfileId";
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE "LanguageProfiles";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
