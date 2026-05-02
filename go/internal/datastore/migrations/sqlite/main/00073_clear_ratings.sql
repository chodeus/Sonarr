-- Ports Migration/073. See ledger row 73.

-- +goose Up
-- +goose StatementBegin
UPDATE "Series" SET "Ratings" = '{}';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Episodes" SET "Ratings" = '{}';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
