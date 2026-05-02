-- Ports Migration/201. HIGH RISK Email settings JSON. No-op. See ledger row 201.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
