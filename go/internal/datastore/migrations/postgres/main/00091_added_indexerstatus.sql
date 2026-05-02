-- Ports Migration/091. See ledger row 91.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "IndexerStatus" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "IndexerId" integer NOT NULL UNIQUE,
    "InitialFailure" timestamp without time zone,
    "MostRecentFailure" timestamp without time zone,
    "EscalationLevel" integer NOT NULL,
    "DisabledTill" timestamp without time zone,
    "LastRssSyncReleaseInfo" text
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_IndexerStatus_IndexerId" ON "IndexerStatus" ("IndexerId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
