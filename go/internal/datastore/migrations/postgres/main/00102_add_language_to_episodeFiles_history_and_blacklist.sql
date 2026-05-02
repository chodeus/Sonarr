-- Ports Migration/102. ADD Language (default 0) on 3 tables. Profile-
-- based backfill no-op for fresh DBs. See ledger row 102.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "EpisodeFiles" ADD COLUMN "Language" integer NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "History" ADD COLUMN "Language" integer NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blacklist" ADD COLUMN "Language" integer NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
