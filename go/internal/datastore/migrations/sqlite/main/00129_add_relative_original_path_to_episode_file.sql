-- Ports Migration/129. See ledger row 129.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "EpisodeFiles" ADD COLUMN "OriginalFilePath" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
