-- Ports Migration/063. See ledger row 63.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "RemotePathMappings" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Host" text NOT NULL,
    "RemotePath" text NOT NULL,
    "LocalPath" text NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
