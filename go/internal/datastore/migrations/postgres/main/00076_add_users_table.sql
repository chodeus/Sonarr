-- Ports Migration/076. See ledger row 76.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Users" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Identifier" text NOT NULL UNIQUE,
    "Username" text NOT NULL UNIQUE,
    "Password" text NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Users_Identifier" ON "Users" ("Identifier");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Users_Username" ON "Users" ("Username");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
