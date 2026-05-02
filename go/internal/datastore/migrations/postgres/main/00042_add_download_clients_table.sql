-- Ports Migration/042. See ledger row 42.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "DownloadClients" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Enable" boolean NOT NULL,
    "Name" text NOT NULL,
    "Implementation" text NOT NULL,
    "Settings" text NOT NULL,
    "ConfigContract" text NOT NULL,
    "Protocol" integer NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
