-- Ports Migration/076. See ledger row 76.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Users" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Identifier" TEXT NOT NULL UNIQUE,
    "Username" TEXT NOT NULL UNIQUE,
    "Password" TEXT NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Users_Identifier" ON "Users" ("Identifier");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Users_Username" ON "Users" ("Username");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
