-- Ports Migration/214. The C# file is empty (// Replaced with 215);
-- this version exists in the ledger but does no work. See ledger row 214.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
