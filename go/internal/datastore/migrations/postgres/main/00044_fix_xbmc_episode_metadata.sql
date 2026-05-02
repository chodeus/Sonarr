-- Ports Migration/044. See ledger row 44.

-- +goose Up
-- +goose StatementBegin
UPDATE "MetadataFiles" SET "Type" = 2 WHERE "Consumer" = 'XbmcMetadata' AND "EpisodeFileId" IS NOT NULL AND "Type" = 4 AND "RelativePath" LIKE '%.nfo';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "MetadataFiles" SET "Type" = 5 WHERE "Consumer" = 'XbmcMetadata' AND "EpisodeFileId" IS NOT NULL AND "Type" = 4;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
