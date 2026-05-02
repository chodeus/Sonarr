-- Ports Migration/161. DELETE Plex notifications + complex ParsedEpisodeInfo
-- JSON fixup (no-op for fresh DBs). See ledger row 161.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Notifications" WHERE "Implementation" = 'PlexHomeTheater';
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM "Notifications" WHERE "Implementation" = 'PlexClient';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
