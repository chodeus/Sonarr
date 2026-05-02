-- Ports Migration/164. See ledger row 164.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Indexers" ADD COLUMN "DownloadClientId" INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
