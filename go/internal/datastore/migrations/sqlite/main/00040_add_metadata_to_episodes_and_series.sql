-- Ports Migration/040. See ledger row 40.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Actors" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Ratings" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Genres" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Certification" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "Ratings" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "Images" TEXT;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
