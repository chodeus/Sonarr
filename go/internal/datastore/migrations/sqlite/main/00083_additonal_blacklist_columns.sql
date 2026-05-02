-- Ports Migration/083. See ledger row 83.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Blacklist" ADD COLUMN "Size" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blacklist" ADD COLUMN "Protocol" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blacklist" ADD COLUMN "Indexer" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blacklist" ADD COLUMN "Message" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blacklist" ADD COLUMN "TorrentInfoHash" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Blacklist" SET "Protocol" = 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
