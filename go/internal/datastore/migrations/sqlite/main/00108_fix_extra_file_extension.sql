-- Ports Migration/108. Drop empty-extension rows + complex C# extension
-- fixup. The simple part runs; the complex part is no-op for fresh DBs.
-- See ledger row 108.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "ExtraFiles" WHERE TRIM("Extension") = '';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
