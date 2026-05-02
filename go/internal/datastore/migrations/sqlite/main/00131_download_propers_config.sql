-- Ports Migration/131. Complex Config-value conversion. No-op. See ledger row 131.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Config" WHERE "Key" = 'autodownloadpropers';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
