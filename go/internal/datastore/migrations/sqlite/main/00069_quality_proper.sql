-- Ports Migration/069. Complex regex-driven naming-format conversion
-- (replaces {Quality Title} tokens). No-op for fresh DBs. See ledger row 69.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
