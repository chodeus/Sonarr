-- Ports the LogDbUpgrade() half of src/NzbDrone.Core/Datastore/Migration/001_initial_setup.cs for Postgres.
-- See go/analysis/migrations/ledger.md row 1 (log-DB column).

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Logs" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Message" text NOT NULL,
    "Time" timestamp without time zone NOT NULL,
    "Logger" text NOT NULL,
    "Method" text,
    "Exception" text,
    "ExceptionType" text,
    "Level" text NOT NULL
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
