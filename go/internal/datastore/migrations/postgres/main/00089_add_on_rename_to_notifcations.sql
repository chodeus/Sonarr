-- Ports Migration/089. See ledger row 89. (Note: ALTER COLUMN to
-- NotNullable from C# is not portable to SQLite; the column stays
-- nullable in SQLite, which matches the captured production schema.)

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "OnRename" boolean;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "OnRename" = "OnDownload" WHERE "Implementation" IN ('PlexServer', 'Xbmc', 'MediaBrowser');
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "OnRename" = false WHERE "Implementation" NOT IN ('PlexServer', 'Xbmc', 'MediaBrowser');
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "OnGrab" = false WHERE "Implementation" = 'PlexServer';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
