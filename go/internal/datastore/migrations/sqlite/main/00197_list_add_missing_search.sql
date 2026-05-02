-- Ports Migration/197. See ledger row 197.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "ImportLists" ADD COLUMN "SearchForMissingEpisodes" INTEGER NOT NULL DEFAULT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
