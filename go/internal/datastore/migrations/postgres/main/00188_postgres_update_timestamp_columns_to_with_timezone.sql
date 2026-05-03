-- Ports Migration/188 (Postgres only). SQLite has no native timezone
-- support, so the SQLite variant is a no-op (keep its TEXT-shape
-- "yyyy-MM-dd HH:mm:ss[.fffffff]" already produced by modernc/sqlite).
--
-- For Postgres, every DateTimeOffset column declared anywhere in the
-- migration tree is converted to `timestamp with time zone`, and the
-- nullability constraint is reapplied to match the C# class fields.
-- FluentMigrator's `AlterColumn(x).AsDateTimeOffset().Nullable()` means
-- "this column's TYPE becomes timestamptz AND it becomes NULLABLE";
-- `.NotNullable()` means TYPE + SET NOT NULL. Both halves matter.
--
-- An earlier abbreviated port of this migration only converted 8 of
-- the 38 columns and never touched nullability — the remaining
-- ~13 columns silently retained stricter NOT NULL than C# HEAD,
-- breaking any downstream service that tries to insert a row with a
-- NULL Date/Added/AirDateUtc/etc. This file restores parity by
-- porting the C# AlterColumn list verbatim.
--
-- ALTER COLUMN ... TYPE timestamptz is idempotent when the column is
-- already timestamptz; DROP NOT NULL is a no-op if already nullable;
-- SET NOT NULL is safe on fresh DBs because every table is empty at
-- this point in the goose ledger. The Commands truncate is preserved
-- as a defensive C# carry-over (no-op for fresh DBs).
--
-- See ledger row 188.

-- +goose Up
-- +goose StatementBegin
DELETE FROM "Commands";
-- +goose StatementEnd

-- Blocklist
-- +goose StatementBegin
ALTER TABLE "Blocklist" ALTER COLUMN "Date" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blocklist" ALTER COLUMN "Date" SET NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blocklist" ALTER COLUMN "PublishedDate" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Blocklist" ALTER COLUMN "PublishedDate" DROP NOT NULL;
-- +goose StatementEnd

-- Commands
-- +goose StatementBegin
ALTER TABLE "Commands" ALTER COLUMN "QueuedAt" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Commands" ALTER COLUMN "QueuedAt" SET NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Commands" ALTER COLUMN "StartedAt" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Commands" ALTER COLUMN "StartedAt" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Commands" ALTER COLUMN "EndedAt" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Commands" ALTER COLUMN "EndedAt" DROP NOT NULL;
-- +goose StatementEnd

-- DownloadClientStatus
-- +goose StatementBegin
ALTER TABLE "DownloadClientStatus" ALTER COLUMN "InitialFailure" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DownloadClientStatus" ALTER COLUMN "InitialFailure" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DownloadClientStatus" ALTER COLUMN "MostRecentFailure" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DownloadClientStatus" ALTER COLUMN "MostRecentFailure" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DownloadClientStatus" ALTER COLUMN "DisabledTill" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DownloadClientStatus" ALTER COLUMN "DisabledTill" DROP NOT NULL;
-- +goose StatementEnd

-- DownloadHistory
-- +goose StatementBegin
ALTER TABLE "DownloadHistory" ALTER COLUMN "Date" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "DownloadHistory" ALTER COLUMN "Date" SET NOT NULL;
-- +goose StatementEnd

-- EpisodeFiles
-- +goose StatementBegin
ALTER TABLE "EpisodeFiles" ALTER COLUMN "DateAdded" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "EpisodeFiles" ALTER COLUMN "DateAdded" SET NOT NULL;
-- +goose StatementEnd

-- Episodes
-- +goose StatementBegin
ALTER TABLE "Episodes" ALTER COLUMN "AirDateUtc" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes" ALTER COLUMN "AirDateUtc" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes" ALTER COLUMN "LastSearchTime" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes" ALTER COLUMN "LastSearchTime" DROP NOT NULL;
-- +goose StatementEnd

-- ExtraFiles
-- +goose StatementBegin
ALTER TABLE "ExtraFiles" ALTER COLUMN "Added" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ExtraFiles" ALTER COLUMN "Added" SET NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ExtraFiles" ALTER COLUMN "LastUpdated" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ExtraFiles" ALTER COLUMN "LastUpdated" SET NOT NULL;
-- +goose StatementEnd

-- History
-- +goose StatementBegin
ALTER TABLE "History" ALTER COLUMN "Date" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "History" ALTER COLUMN "Date" SET NOT NULL;
-- +goose StatementEnd

-- ImportListStatus
-- +goose StatementBegin
ALTER TABLE "ImportListStatus" ALTER COLUMN "InitialFailure" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ImportListStatus" ALTER COLUMN "InitialFailure" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ImportListStatus" ALTER COLUMN "MostRecentFailure" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ImportListStatus" ALTER COLUMN "MostRecentFailure" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ImportListStatus" ALTER COLUMN "DisabledTill" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ImportListStatus" ALTER COLUMN "DisabledTill" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ImportListStatus" ALTER COLUMN "LastInfoSync" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ImportListStatus" ALTER COLUMN "LastInfoSync" DROP NOT NULL;
-- +goose StatementEnd

-- IndexerStatus
-- +goose StatementBegin
ALTER TABLE "IndexerStatus" ALTER COLUMN "InitialFailure" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "IndexerStatus" ALTER COLUMN "InitialFailure" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "IndexerStatus" ALTER COLUMN "MostRecentFailure" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "IndexerStatus" ALTER COLUMN "MostRecentFailure" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "IndexerStatus" ALTER COLUMN "DisabledTill" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "IndexerStatus" ALTER COLUMN "DisabledTill" DROP NOT NULL;
-- +goose StatementEnd

-- MetadataFiles
-- +goose StatementBegin
ALTER TABLE "MetadataFiles" ALTER COLUMN "LastUpdated" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "MetadataFiles" ALTER COLUMN "LastUpdated" SET NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "MetadataFiles" ALTER COLUMN "Added" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "MetadataFiles" ALTER COLUMN "Added" DROP NOT NULL;
-- +goose StatementEnd

-- PendingReleases
-- +goose StatementBegin
ALTER TABLE "PendingReleases" ALTER COLUMN "Added" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "PendingReleases" ALTER COLUMN "Added" SET NOT NULL;
-- +goose StatementEnd

-- ScheduledTasks
-- +goose StatementBegin
ALTER TABLE "ScheduledTasks" ALTER COLUMN "LastExecution" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ScheduledTasks" ALTER COLUMN "LastExecution" SET NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ScheduledTasks" ALTER COLUMN "LastStartTime" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ScheduledTasks" ALTER COLUMN "LastStartTime" DROP NOT NULL;
-- +goose StatementEnd

-- Series
-- +goose StatementBegin
ALTER TABLE "Series" ALTER COLUMN "LastInfoSync" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ALTER COLUMN "LastInfoSync" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ALTER COLUMN "LastDiskSync" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ALTER COLUMN "LastDiskSync" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ALTER COLUMN "FirstAired" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ALTER COLUMN "FirstAired" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ALTER COLUMN "NextAiring" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ALTER COLUMN "NextAiring" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ALTER COLUMN "Added" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ALTER COLUMN "Added" DROP NOT NULL;
-- +goose StatementEnd

-- SubtitleFiles
-- +goose StatementBegin
ALTER TABLE "SubtitleFiles" ALTER COLUMN "LastUpdated" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "SubtitleFiles" ALTER COLUMN "LastUpdated" SET NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "SubtitleFiles" ALTER COLUMN "Added" TYPE timestamp with time zone;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "SubtitleFiles" ALTER COLUMN "Added" DROP NOT NULL;
-- +goose StatementEnd

-- VersionInfo (note: goose uses goose_db_version, not VersionInfo, but
-- the table existed at C# v188 and gets the same TYPE conversion. The
-- Go port doesn't create VersionInfo; ALTER on a missing table would
-- error, so the C# VersionInfo line is intentionally omitted here.)

-- +goose Down
SELECT 'rollback not supported' WHERE false;
