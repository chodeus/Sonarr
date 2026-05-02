-- Ports Migration/140. See ledger row 140.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Config" WHERE "Key" IN ('folderchmod', 'chownuser');
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
