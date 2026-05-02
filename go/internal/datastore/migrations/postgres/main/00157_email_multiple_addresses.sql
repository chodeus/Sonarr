-- Ports Migration/157. HIGH RISK Email-settings JSON shape. No-op. See ledger row 157.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
