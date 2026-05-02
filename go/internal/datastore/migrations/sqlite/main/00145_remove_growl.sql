-- Ports Migration/145. See ledger row 145.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Notifications" WHERE "Implementation" = 'Growl';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
