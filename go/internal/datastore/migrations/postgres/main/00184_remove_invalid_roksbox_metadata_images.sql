-- Ports Migration/184. See ledger row 184.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "MetadataFiles" WHERE "Consumer" = 'RoksboxMetadata' AND "Type" = 5 AND ("RelativePath" LIKE '%/metadata/%' OR "RelativePath" LIKE '%\\metadata\\%');
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
