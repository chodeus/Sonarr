-- Ports Migration/122. HIGH RISK Profile JSON. No-op. See ledger row 122.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
