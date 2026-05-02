-- Ports Migration/094. See ledger row 94.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "TvMazeId" integer DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_Series_TvMazeId" ON "Series" ("TvMazeId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
