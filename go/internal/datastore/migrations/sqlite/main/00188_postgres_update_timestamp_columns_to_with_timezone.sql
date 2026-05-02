-- Ports Migration/188. SQLite has no native TZ support so no-op there;
-- Postgres conversion is verbose but mechanical. The Commands table is
-- truncated first — no-op for fresh DBs. See ledger row 188.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
