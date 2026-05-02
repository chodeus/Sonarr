-- Ports Migration/164. See ledger row 164.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Indexers" ADD COLUMN "DownloadClientId" integer NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
