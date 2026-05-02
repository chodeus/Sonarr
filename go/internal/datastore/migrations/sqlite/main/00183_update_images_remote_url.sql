-- Ports Migration/183. JSON key rename "url" → "remoteUrl" in 3 columns.
-- See ledger row 183.

-- +goose Up
-- +goose StatementBegin
UPDATE "Episodes" SET "Images" = REPLACE("Images", '"url"', '"remoteUrl"');
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Series" SET "Images" = REPLACE("Images", '"url"', '"remoteUrl"');
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Series" SET "Actors" = REPLACE("Actors", '"url"', '"remoteUrl"');
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Series" SET "Seasons" = REPLACE("Seasons", '"url"', '"remoteUrl"');
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
