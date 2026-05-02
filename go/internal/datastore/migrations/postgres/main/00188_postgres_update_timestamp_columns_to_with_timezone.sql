-- Ports Migration/188. SQLite has no native TZ support so no-op there;
-- Postgres conversion is verbose but mechanical. The Commands table is
-- truncated first — no-op for fresh DBs. See ledger row 188.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Commands";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blocklist" ALTER COLUMN "Date" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blocklist" ALTER COLUMN "PublishedDate" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Commands" ALTER COLUMN "QueuedAt" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Commands" ALTER COLUMN "StartedAt" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Commands" ALTER COLUMN "EndedAt" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DownloadClientStatus" ALTER COLUMN "InitialFailure" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DownloadClientStatus" ALTER COLUMN "MostRecentFailure" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DownloadClientStatus" ALTER COLUMN "DisabledTill" TYPE timestamp with time zone;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
