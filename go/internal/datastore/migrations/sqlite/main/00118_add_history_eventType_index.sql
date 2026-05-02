-- Ports Migration/118. See ledger row 118.

-- +goose Up
-- +goose StatementBegin
CREATE INDEX "IX_History_EventType" ON "History" ("EventType");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
