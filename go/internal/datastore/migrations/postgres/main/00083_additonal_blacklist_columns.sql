-- Ports Migration/083. See ledger row 83.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Blacklist" ADD COLUMN "Size" integer;
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
