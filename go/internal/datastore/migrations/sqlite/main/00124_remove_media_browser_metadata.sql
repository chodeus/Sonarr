-- Ports Migration/124. See ledger row 124.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Metadata" WHERE "Implementation" = 'MediaBrowserMetadata';
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM "MetadataFiles" WHERE "Consumer" = 'MediaBrowserMetadata';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
