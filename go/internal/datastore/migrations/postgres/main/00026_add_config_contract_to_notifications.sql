-- Ports Migration/026 for Postgres. See ledger row 26.

-- +goose Up
-- +goose StatementBegin
UPDATE "Notifications" SET "ConfigContract" = 'EmailSettings' WHERE "Implementation" = 'Email';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "ConfigContract" = 'GrowlSettings' WHERE "Implementation" = 'Growl';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "ConfigContract" = 'NotifyMyAndroidSettings' WHERE "Implementation" = 'NotifyMyAndroid';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "ConfigContract" = 'PlexClientSettings' WHERE "Implementation" = 'PlexClient';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "ConfigContract" = 'PlexServerSettings' WHERE "Implementation" = 'PlexServer';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "ConfigContract" = 'ProwlSettings' WHERE "Implementation" = 'Prowl';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "ConfigContract" = 'PushBulletSettings' WHERE "Implementation" = 'PushBullet';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "ConfigContract" = 'PushoverSettings' WHERE "Implementation" = 'Pushover';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "ConfigContract" = 'XbmcSettings' WHERE "Implementation" = 'Xbmc';
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM "Notifications" WHERE "ConfigContract" IS NULL;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
