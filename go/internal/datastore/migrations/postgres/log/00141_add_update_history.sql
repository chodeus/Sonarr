-- Ports Migration/141 (LogDbUpgrade). See ledger row 141 (log).

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "UpdateHistory" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Date" timestamp without time zone NOT NULL,
    "Version" text NOT NULL,
    "EventType" integer NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_UpdateHistory_Date" ON "UpdateHistory" ("Date");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
