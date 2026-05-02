-- Ports Migration/034. SQLite ALTER COLUMN limitations: this migration's
-- intent is to relax NOT NULL on ImdbId/TitleSlug and remove TvRageId's
-- uniqueness. SQLite doesn't support ALTER COLUMN for nullability or
-- uniqueness changes without a full table recreate. The runtime
-- application-layer deduplication handles the practical cases without
-- a schema-level relaxation; the captured production schema at v230 keeps
-- the inline UNIQUE constraints. No-op here. See ledger row 34.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
