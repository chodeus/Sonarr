-- Ports Migration/115. See ledger row 115.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "DownloadClientStatus" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "ProviderId" integer NOT NULL UNIQUE,
    "InitialFailure" timestamp without time zone,
    "MostRecentFailure" timestamp without time zone,
    "EscalationLevel" integer NOT NULL,
    "DisabledTill" timestamp without time zone
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_DownloadClientStatus_ProviderId" ON "DownloadClientStatus" ("ProviderId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
