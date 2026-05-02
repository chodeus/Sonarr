-- Ports Migration/155. HIGH RISK LanguageProfile JSON. No-op. See ledger row 155.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
