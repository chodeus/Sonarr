-- Ports Migration/195. Complex parser-driven LanguageTags backfill. No-op
-- for fresh DBs. See ledger row 195.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
