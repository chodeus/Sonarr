-- Ports Migration/126. See ledger row 126.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "CustomFilters" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Type" text NOT NULL,
    "Label" text NOT NULL,
    "Filters" text NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
