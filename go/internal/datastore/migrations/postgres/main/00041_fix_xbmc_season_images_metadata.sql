-- Ports Migration/041. See ledger row 41.

-- +goose Up
-- +goose StatementBegin
UPDATE "MetadataFiles" SET "Type" = 4 WHERE "Consumer" = 'XbmcMetadata' AND "SeasonNumber" IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
