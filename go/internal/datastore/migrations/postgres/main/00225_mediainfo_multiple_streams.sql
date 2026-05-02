-- Ports Migration/225. HIGH RISK MediaInfo full reshape (split into
-- streams). No-op for fresh DBs; cumulativity-sensitive 148 → 163 → 225
-- chain. See ledger row 225.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
