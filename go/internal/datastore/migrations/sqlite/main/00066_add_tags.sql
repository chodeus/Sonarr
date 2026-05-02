-- Ports Migration/066. See ledger row 66.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Tags" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Label" TEXT NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Tags" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "Tags" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Series" SET "Tags" = '[]';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Notifications" SET "Tags" = '[]';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
