-- Ports Migration/194. See ledger row 194.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "NotificationStatus" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "ProviderId" integer NOT NULL UNIQUE,
    "InitialFailure" timestamp with time zone,
    "MostRecentFailure" timestamp with time zone,
    "EscalationLevel" integer NOT NULL,
    "DisabledTill" timestamp with time zone
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_NotificationStatus_ProviderId" ON "NotificationStatus" ("ProviderId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
