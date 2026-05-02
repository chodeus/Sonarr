-- Ports Migration/167. See ledger row 167.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "TvdbId" INTEGER;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
