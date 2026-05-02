-- Ports Migration/129. See ledger row 129.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "EpisodeFiles" ADD COLUMN "OriginalFilePath" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
