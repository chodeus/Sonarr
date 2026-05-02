-- Ports Migration/066. See ledger row 66.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Tags" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Label" text NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Tags" text;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "Tags" text;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Series" SET "Tags" = '[]';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "Tags" = '[]';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
