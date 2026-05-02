-- Ports Migration/217. See ledger row 217.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "MalIds" TEXT DEFAULT '[]';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "AniListIds" TEXT DEFAULT '[]';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
