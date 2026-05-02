-- Ports Migration/175. HIGH RISK — wrap singleton Language column into
-- Languages JSON array on EpisodeFiles, History, Blocklist. Real SQL
-- (cumulativity-sensitive). See ledger row 175.

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

-- +goose Down
SELECT 'rollback not supported' WHERE false;
