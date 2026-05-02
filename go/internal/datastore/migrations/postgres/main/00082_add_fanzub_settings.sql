-- Ports Migration/082. See ledger row 82.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "ConfigContract" = 'FanzubSettings' WHERE "Implementation" = 'Fanzub' AND "ConfigContract" = 'NullConfig';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
