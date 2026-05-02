-- Ports Migration/192. ALTER ImportListExclusions.TvdbId TEXT → INT.
-- SQLite has dynamic typing — no-op. Postgres needs explicit cast.
-- See ledger row 192.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "ImportListExclusions" ALTER COLUMN "TvdbId" TYPE INTEGER USING "TvdbId"::integer;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
