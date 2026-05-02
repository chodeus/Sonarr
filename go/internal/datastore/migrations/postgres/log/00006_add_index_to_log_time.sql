-- Ports src/NzbDrone.Core/Datastore/Migration/006_add_index_to_log_time.cs (LogDbUpgrade) for Postgres.
-- See go/analysis/migrations/ledger.md row 6 (log-DB column).

-- +goose Up
-- +goose StatementBegin
DROP TABLE IF EXISTS "Logs";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "Logs" (
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
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS "IX_Logs_Time" ON "Logs" ("Time" ASC);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
