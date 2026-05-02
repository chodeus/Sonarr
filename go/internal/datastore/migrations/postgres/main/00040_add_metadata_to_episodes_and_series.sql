-- Ports Migration/040. See ledger row 40.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Actors" text;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Ratings" text;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Genres" text;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Certification" text;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "Ratings" text;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "Images" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
