-- Ports Migration/123. See ledger row 123.

-- +goose Up
-- +goose StatementBegin
CREATE INDEX "IX_History_SeriesId" ON "History" ("SeriesId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
