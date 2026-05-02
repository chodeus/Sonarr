-- Ports Migration/218. HIGH RISK Telegram settings JSON. No-op. See ledger row 218.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
