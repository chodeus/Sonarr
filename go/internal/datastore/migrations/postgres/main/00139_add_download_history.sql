-- Ports Migration/139. See ledger row 139.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "DownloadHistory" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "EventType" integer NOT NULL,
    "SeriesId" integer NOT NULL,
    "DownloadId" text NOT NULL,
    "SourceTitle" text NOT NULL,
    "Date" timestamp without time zone NOT NULL,
    "Protocol" integer,
    "IndexerId" integer,
    "DownloadClientId" integer,
    "Release" text,
    "Data" text
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_DownloadHistory_EventType" ON "DownloadHistory" ("EventType");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_DownloadHistory_SeriesId" ON "DownloadHistory" ("SeriesId");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_DownloadHistory_DownloadId" ON "DownloadHistory" ("DownloadId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
