-- Ports Migration/160. See ledger row 160.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Blacklist" RENAME TO "Blocklist";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
