-- Ports Migration/188 (LogDbUpgrade). SQLite no-op (no TZ types).
-- Postgres converts Logs.Time + UpdateHistory.Date to TIMESTAMPTZ.
-- VersionInfo is goose-managed, not Sonarr's, so we skip the C# migration's
-- VersionInfo.AppliedOn alter. See ledger row 188 (log-DB column).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Logs" ALTER COLUMN "Time" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "UpdateHistory" ALTER COLUMN "Date" TYPE timestamp with time zone;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
