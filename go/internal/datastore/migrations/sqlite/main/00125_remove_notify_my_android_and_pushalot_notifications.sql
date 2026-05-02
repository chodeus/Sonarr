-- Ports Migration/125. See ledger row 125.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Notifications" WHERE "Implementation" = 'NotifyMyAndroid';
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM "Notifications" WHERE "Implementation" = 'Pushalot';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
