-- Ports Migration/172. See ledger row 172.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Indexers" ADD COLUMN "SeasonSearchMaximumSingleEpisodeAge" INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
