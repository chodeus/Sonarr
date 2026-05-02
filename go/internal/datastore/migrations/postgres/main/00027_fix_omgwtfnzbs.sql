-- Ports Migration/027. Idempotent fixup for Omgwtfnzbs indexers. See ledger row 27.

-- +goose Up
-- +goose StatementBegin
UPDATE "Indexers" SET "ConfigContract" = 'OmgwtfnzbsSettings' WHERE "Implementation" = 'Omgwtfnzbs';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Indexers" SET "Settings" = '{}' WHERE "Implementation" = 'Omgwtfnzbs' AND "Settings" IS NULL;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Indexers" SET "Settings" = '{}' WHERE "Implementation" = 'Omgwtfnzbs' AND "Settings" = '';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
