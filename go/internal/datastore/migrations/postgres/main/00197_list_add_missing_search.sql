-- Ports Migration/197. See ledger row 197.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "ImportLists" ADD COLUMN "SearchForMissingEpisodes" boolean NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
