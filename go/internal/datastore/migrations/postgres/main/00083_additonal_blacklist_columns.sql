-- Ports Migration/083. C# declares Size as AsInt64() (bigint); the
-- original Postgres port used `integer` which truncates at ~2GB.
-- Fixed to bigint 2026-05-02. SQLite INTEGER is dynamic-affinity so the
-- sibling needs no change.
-- See ledger row 83.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Blacklist" ADD COLUMN "Size" bigint;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blacklist" ADD COLUMN "Protocol" integer;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blacklist" ADD COLUMN "Indexer" text;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blacklist" ADD COLUMN "Message" text;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blacklist" ADD COLUMN "TorrentInfoHash" text;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Blacklist" SET "Protocol" = 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
