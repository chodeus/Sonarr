-- Ports Migration/078. See ledger row 78.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Commands" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Name" text NOT NULL,
    "Body" text NOT NULL,
    "Priority" integer NOT NULL,
    "Status" integer NOT NULL,
    "QueuedAt" timestamp without time zone NOT NULL,
    "StartedAt" timestamp without time zone,
    "EndedAt" timestamp without time zone,
    "Duration" text,
    "Exception" text,
    "Trigger" integer NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
