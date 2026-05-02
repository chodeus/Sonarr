--
-- PostgreSQL database dump
--

\restrict 0uKplV1LSsgGBpi3yFNvFyoJAQVgTpZVrDXUqhxJuiwkbg3iZtbPgmbwvzvwNEc

-- Dumped from database version 16.13 (Homebrew)
-- Dumped by pg_dump version 16.13 (Homebrew)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: AutoTagging; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."AutoTagging" (
    "Id" integer NOT NULL,
    "Name" text NOT NULL,
    "Specifications" text DEFAULT '[]'::text NOT NULL,
    "RemoveTagsAutomatically" boolean DEFAULT false NOT NULL,
    "Tags" text DEFAULT '[]'::text NOT NULL
);


--
-- Name: AutoTagging_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."AutoTagging" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."AutoTagging_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: Blocklist; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."Blocklist" (
    "Id" integer NOT NULL,
    "SeriesId" integer NOT NULL,
    "EpisodeIds" text NOT NULL,
    "SourceTitle" text NOT NULL,
    "Quality" text NOT NULL,
    "Date" timestamp with time zone NOT NULL,
    "PublishedDate" timestamp with time zone,
    "Size" bigint,
    "Protocol" integer,
    "Indexer" text,
    "Message" text,
    "TorrentInfoHash" text,
    "Languages" text DEFAULT '[]'::text NOT NULL,
    "IndexerFlags" integer DEFAULT 0 NOT NULL,
    "ReleaseType" integer DEFAULT 0 NOT NULL,
    "Source" text
);


--
-- Name: Blacklist_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."Blocklist" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."Blacklist_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: Commands; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."Commands" (
    "Id" integer NOT NULL,
    "Name" text NOT NULL,
    "Body" text NOT NULL,
    "Priority" integer NOT NULL,
    "Status" integer NOT NULL,
    "QueuedAt" timestamp with time zone NOT NULL,
    "StartedAt" timestamp with time zone,
    "EndedAt" timestamp with time zone,
    "Duration" text,
    "Exception" text,
    "Trigger" integer NOT NULL,
    "Result" integer DEFAULT 1 NOT NULL
);


--
-- Name: Commands_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."Commands" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."Commands_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: Config; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."Config" (
    "Id" integer NOT NULL,
    "Key" text NOT NULL,
    "Value" text NOT NULL
);


--
-- Name: Config_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."Config" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."Config_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: CustomFilters; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."CustomFilters" (
    "Id" integer NOT NULL,
    "Type" text NOT NULL,
    "Label" text NOT NULL,
    "Filters" text NOT NULL
);


--
-- Name: CustomFilters_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."CustomFilters" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."CustomFilters_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: CustomFormats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."CustomFormats" (
    "Id" integer NOT NULL,
    "Name" text NOT NULL,
    "Specifications" text DEFAULT '[]'::text NOT NULL,
    "IncludeCustomFormatWhenRenaming" boolean DEFAULT false NOT NULL
);


--
-- Name: CustomFormats_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."CustomFormats" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."CustomFormats_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: DelayProfiles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."DelayProfiles" (
    "Id" integer NOT NULL,
    "EnableUsenet" boolean NOT NULL,
    "EnableTorrent" boolean NOT NULL,
    "PreferredProtocol" integer NOT NULL,
    "UsenetDelay" integer NOT NULL,
    "TorrentDelay" integer NOT NULL,
    "Order" integer NOT NULL,
    "Tags" text NOT NULL,
    "BypassIfHighestQuality" boolean DEFAULT false NOT NULL,
    "BypassIfAboveCustomFormatScore" boolean DEFAULT false NOT NULL,
    "MinimumCustomFormatScore" integer
);


--
-- Name: DelayProfiles_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."DelayProfiles" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."DelayProfiles_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: DownloadClientStatus; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."DownloadClientStatus" (
    "Id" integer NOT NULL,
    "ProviderId" integer NOT NULL,
    "InitialFailure" timestamp with time zone,
    "MostRecentFailure" timestamp with time zone,
    "EscalationLevel" integer NOT NULL,
    "DisabledTill" timestamp with time zone
);


--
-- Name: DownloadClientStatus_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."DownloadClientStatus" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."DownloadClientStatus_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: DownloadClients; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."DownloadClients" (
    "Id" integer NOT NULL,
    "Enable" boolean NOT NULL,
    "Name" text NOT NULL,
    "Implementation" text NOT NULL,
    "Settings" text NOT NULL,
    "ConfigContract" text NOT NULL,
    "Priority" integer DEFAULT 1 NOT NULL,
    "RemoveCompletedDownloads" boolean DEFAULT true NOT NULL,
    "RemoveFailedDownloads" boolean DEFAULT true NOT NULL,
    "Tags" text
);


--
-- Name: DownloadClients_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."DownloadClients" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."DownloadClients_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: DownloadHistory; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."DownloadHistory" (
    "Id" integer NOT NULL,
    "EventType" integer NOT NULL,
    "SeriesId" integer NOT NULL,
    "DownloadId" text NOT NULL,
    "SourceTitle" text NOT NULL,
    "Date" timestamp with time zone NOT NULL,
    "Protocol" integer,
    "IndexerId" integer,
    "DownloadClientId" integer,
    "Release" text,
    "Data" text
);


--
-- Name: DownloadHistory_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."DownloadHistory" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."DownloadHistory_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: EpisodeFiles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."EpisodeFiles" (
    "Id" integer NOT NULL,
    "SeriesId" integer NOT NULL,
    "Quality" text NOT NULL,
    "Size" bigint NOT NULL,
    "DateAdded" timestamp with time zone NOT NULL,
    "SeasonNumber" integer NOT NULL,
    "SceneName" text,
    "ReleaseGroup" text,
    "MediaInfo" text,
    "RelativePath" text,
    "OriginalFilePath" text,
    "Languages" text DEFAULT '[]'::text NOT NULL,
    "IndexerFlags" integer DEFAULT 0 NOT NULL,
    "ReleaseType" integer DEFAULT 0 NOT NULL,
    "ReleaseHash" text
);


--
-- Name: EpisodeFiles_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."EpisodeFiles" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."EpisodeFiles_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: Episodes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."Episodes" (
    "Id" integer NOT NULL,
    "SeriesId" integer NOT NULL,
    "SeasonNumber" integer NOT NULL,
    "EpisodeNumber" integer NOT NULL,
    "Title" text,
    "Overview" text,
    "EpisodeFileId" integer,
    "AbsoluteEpisodeNumber" integer,
    "SceneAbsoluteEpisodeNumber" integer,
    "SceneSeasonNumber" integer,
    "SceneEpisodeNumber" integer,
    "Monitored" boolean,
    "AirDateUtc" timestamp with time zone,
    "AirDate" text,
    "Ratings" text,
    "Images" text,
    "UnverifiedSceneNumbering" boolean DEFAULT false NOT NULL,
    "LastSearchTime" timestamp with time zone,
    "AiredAfterSeasonNumber" integer,
    "AiredBeforeSeasonNumber" integer,
    "AiredBeforeEpisodeNumber" integer,
    "TvdbId" integer,
    "Runtime" integer DEFAULT 0 NOT NULL,
    "FinaleType" text
);


--
-- Name: Episodes_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."Episodes" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."Episodes_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: ExtraFiles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."ExtraFiles" (
    "Id" integer NOT NULL,
    "SeriesId" integer NOT NULL,
    "SeasonNumber" integer NOT NULL,
    "EpisodeFileId" integer NOT NULL,
    "RelativePath" text NOT NULL,
    "Extension" text NOT NULL,
    "Added" timestamp with time zone NOT NULL,
    "LastUpdated" timestamp with time zone NOT NULL
);


--
-- Name: ExtraFiles_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."ExtraFiles" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."ExtraFiles_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: History; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."History" (
    "Id" integer NOT NULL,
    "EpisodeId" integer NOT NULL,
    "SeriesId" integer NOT NULL,
    "SourceTitle" text NOT NULL,
    "Date" timestamp with time zone NOT NULL,
    "Quality" text NOT NULL,
    "Data" text NOT NULL,
    "EventType" integer,
    "DownloadId" text,
    "Languages" text DEFAULT '[]'::text NOT NULL
);


--
-- Name: History_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."History" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."History_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: ImportListExclusions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."ImportListExclusions" (
    "Id" integer NOT NULL,
    "TvdbId" integer NOT NULL,
    "Title" text NOT NULL
);


--
-- Name: ImportListExclusions_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."ImportListExclusions" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."ImportListExclusions_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: ImportListItems; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."ImportListItems" (
    "Id" integer NOT NULL,
    "ImportListId" integer NOT NULL,
    "Title" text NOT NULL,
    "TvdbId" integer NOT NULL,
    "Year" integer,
    "TmdbId" integer,
    "ImdbId" text,
    "MalId" integer,
    "AniListId" integer,
    "ReleaseDate" timestamp with time zone
);


--
-- Name: ImportListItems_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."ImportListItems" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."ImportListItems_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: ImportListStatus; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."ImportListStatus" (
    "Id" integer NOT NULL,
    "ProviderId" integer NOT NULL,
    "InitialFailure" timestamp with time zone,
    "MostRecentFailure" timestamp with time zone,
    "EscalationLevel" integer NOT NULL,
    "DisabledTill" timestamp with time zone,
    "LastInfoSync" timestamp with time zone,
    "HasRemovedItemSinceLastClean" boolean DEFAULT false NOT NULL
);


--
-- Name: ImportListStatus_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."ImportListStatus" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."ImportListStatus_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: ImportLists; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."ImportLists" (
    "Id" integer NOT NULL,
    "Name" text NOT NULL,
    "Implementation" text NOT NULL,
    "Settings" text,
    "ConfigContract" text,
    "EnableAutomaticAdd" boolean,
    "RootFolderPath" text NOT NULL,
    "ShouldMonitor" integer NOT NULL,
    "QualityProfileId" integer NOT NULL,
    "Tags" text,
    "SeriesType" integer DEFAULT 0 NOT NULL,
    "SeasonFolder" boolean DEFAULT true NOT NULL,
    "SearchForMissingEpisodes" boolean DEFAULT true NOT NULL,
    "MonitorNewItems" integer DEFAULT 0 NOT NULL
);


--
-- Name: ImportLists_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."ImportLists" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."ImportLists_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: IndexerStatus; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."IndexerStatus" (
    "Id" integer NOT NULL,
    "ProviderId" integer NOT NULL,
    "InitialFailure" timestamp with time zone,
    "MostRecentFailure" timestamp with time zone,
    "EscalationLevel" integer NOT NULL,
    "DisabledTill" timestamp with time zone,
    "LastRssSyncReleaseInfo" text
);


--
-- Name: IndexerStatus_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."IndexerStatus" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."IndexerStatus_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: Indexers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."Indexers" (
    "Id" integer NOT NULL,
    "Name" text NOT NULL,
    "Implementation" text NOT NULL,
    "Settings" text,
    "ConfigContract" text,
    "EnableRss" boolean,
    "EnableAutomaticSearch" boolean,
    "EnableInteractiveSearch" boolean NOT NULL,
    "Priority" integer DEFAULT 25 NOT NULL,
    "Tags" text,
    "DownloadClientId" integer DEFAULT 0 NOT NULL,
    "SeasonSearchMaximumSingleEpisodeAge" integer DEFAULT 0 NOT NULL
);


--
-- Name: Indexers_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."Indexers" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."Indexers_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: Metadata; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."Metadata" (
    "Id" integer NOT NULL,
    "Enable" boolean NOT NULL,
    "Name" text NOT NULL,
    "Implementation" text NOT NULL,
    "Settings" text NOT NULL,
    "ConfigContract" text NOT NULL
);


--
-- Name: MetadataFiles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."MetadataFiles" (
    "Id" integer NOT NULL,
    "SeriesId" integer NOT NULL,
    "Consumer" text NOT NULL,
    "Type" integer NOT NULL,
    "RelativePath" text NOT NULL,
    "LastUpdated" timestamp with time zone NOT NULL,
    "SeasonNumber" integer,
    "EpisodeFileId" integer,
    "Hash" text,
    "Added" timestamp with time zone,
    "Extension" text NOT NULL
);


--
-- Name: MetadataFiles_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."MetadataFiles" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."MetadataFiles_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: Metadata_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."Metadata" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."Metadata_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: NamingConfig; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."NamingConfig" (
    "Id" integer NOT NULL,
    "MultiEpisodeStyle" integer NOT NULL,
    "RenameEpisodes" boolean,
    "StandardEpisodeFormat" text,
    "DailyEpisodeFormat" text,
    "SeasonFolderFormat" text,
    "SeriesFolderFormat" text,
    "AnimeEpisodeFormat" text,
    "ReplaceIllegalCharacters" boolean DEFAULT true NOT NULL,
    "SpecialsFolderFormat" text,
    "ColonReplacementFormat" integer DEFAULT 4 NOT NULL,
    "CustomColonReplacementFormat" text DEFAULT ''::text NOT NULL
);


--
-- Name: NamingConfig_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."NamingConfig" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."NamingConfig_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: NotificationStatus; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."NotificationStatus" (
    "Id" integer NOT NULL,
    "ProviderId" integer NOT NULL,
    "InitialFailure" timestamp with time zone,
    "MostRecentFailure" timestamp with time zone,
    "EscalationLevel" integer NOT NULL,
    "DisabledTill" timestamp with time zone
);


--
-- Name: NotificationStatus_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."NotificationStatus" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."NotificationStatus_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: Notifications; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."Notifications" (
    "Id" integer NOT NULL,
    "Name" text NOT NULL,
    "OnGrab" boolean NOT NULL,
    "OnDownload" boolean NOT NULL,
    "Settings" text NOT NULL,
    "Implementation" text NOT NULL,
    "ConfigContract" text,
    "OnUpgrade" boolean,
    "Tags" text,
    "OnRename" boolean NOT NULL,
    "OnHealthIssue" boolean DEFAULT false NOT NULL,
    "IncludeHealthWarnings" boolean DEFAULT false NOT NULL,
    "OnSeriesDelete" boolean DEFAULT false NOT NULL,
    "OnEpisodeFileDelete" boolean DEFAULT false NOT NULL,
    "OnEpisodeFileDeleteForUpgrade" boolean DEFAULT true NOT NULL,
    "OnApplicationUpdate" boolean DEFAULT false NOT NULL,
    "OnManualInteractionRequired" boolean DEFAULT false NOT NULL,
    "OnSeriesAdd" boolean DEFAULT false NOT NULL,
    "OnHealthRestored" boolean DEFAULT false NOT NULL,
    "OnImportComplete" boolean DEFAULT false NOT NULL
);


--
-- Name: Notifications_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."Notifications" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."Notifications_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: PendingReleases; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."PendingReleases" (
    "Id" integer NOT NULL,
    "SeriesId" integer NOT NULL,
    "Title" text NOT NULL,
    "Added" timestamp with time zone NOT NULL,
    "ParsedEpisodeInfo" text NOT NULL,
    "Release" text NOT NULL,
    "Reason" integer DEFAULT 0 NOT NULL,
    "AdditionalInfo" text
);


--
-- Name: PendingReleases_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."PendingReleases" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."PendingReleases_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: QualityDefinitions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."QualityDefinitions" (
    "Id" integer NOT NULL,
    "Quality" integer NOT NULL,
    "Title" text NOT NULL
);


--
-- Name: QualityDefinitions_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."QualityDefinitions" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."QualityDefinitions_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: QualityProfiles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."QualityProfiles" (
    "Id" integer NOT NULL,
    "Name" text NOT NULL,
    "Cutoff" integer NOT NULL,
    "Items" text NOT NULL,
    "UpgradeAllowed" boolean,
    "FormatItems" text DEFAULT '[]'::text NOT NULL,
    "MinFormatScore" integer DEFAULT 0 NOT NULL,
    "CutoffFormatScore" integer DEFAULT 0 NOT NULL,
    "MinUpgradeFormatScore" integer DEFAULT 1 NOT NULL
);


--
-- Name: QualityProfiles_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."QualityProfiles" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."QualityProfiles_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: ReleaseProfiles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."ReleaseProfiles" (
    "Id" integer NOT NULL,
    "Required" text,
    "Ignored" text,
    "Tags" text NOT NULL,
    "Enabled" boolean DEFAULT true NOT NULL,
    "Name" text,
    "ExcludedTags" text DEFAULT '[]'::text NOT NULL,
    "IndexerIds" text DEFAULT '[]'::text NOT NULL,
    "AirDateRestriction" boolean DEFAULT false NOT NULL,
    "AirDateGracePeriod" integer DEFAULT 0 NOT NULL
);


--
-- Name: RemotePathMappings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."RemotePathMappings" (
    "Id" integer NOT NULL,
    "Host" text NOT NULL,
    "RemotePath" text NOT NULL,
    "LocalPath" text NOT NULL
);


--
-- Name: RemotePathMappings_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."RemotePathMappings" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."RemotePathMappings_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: Restrictions_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."ReleaseProfiles" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."Restrictions_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: RootFolders; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."RootFolders" (
    "Id" integer NOT NULL,
    "Path" text NOT NULL
);


--
-- Name: RootFolders_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."RootFolders" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."RootFolders_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: SceneMappings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."SceneMappings" (
    "Id" integer NOT NULL,
    "TvdbId" integer NOT NULL,
    "SeasonNumber" integer,
    "SearchTerm" text NOT NULL,
    "ParseTerm" text NOT NULL,
    "Title" text,
    "Type" text,
    "SceneSeasonNumber" integer,
    "FilterRegex" text,
    "SceneOrigin" text,
    "SearchMode" integer,
    "Comment" text,
    "MappingId" text
);


--
-- Name: SceneMappings_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."SceneMappings" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."SceneMappings_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: ScheduledTasks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."ScheduledTasks" (
    "Id" integer NOT NULL,
    "TypeName" text NOT NULL,
    "Interval" integer NOT NULL,
    "LastExecution" timestamp with time zone NOT NULL,
    "LastStartTime" timestamp with time zone
);


--
-- Name: ScheduledTasks_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."ScheduledTasks" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."ScheduledTasks_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: Series; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."Series" (
    "Id" integer NOT NULL,
    "TvdbId" integer NOT NULL,
    "TvRageId" integer NOT NULL,
    "ImdbId" text,
    "Title" text NOT NULL,
    "TitleSlug" text,
    "CleanTitle" text NOT NULL,
    "Status" integer NOT NULL,
    "Overview" text,
    "AirTime" text,
    "Images" text NOT NULL,
    "Path" text NOT NULL,
    "Monitored" boolean NOT NULL,
    "SeasonFolder" boolean NOT NULL,
    "LastInfoSync" timestamp with time zone,
    "LastDiskSync" timestamp with time zone,
    "Runtime" integer NOT NULL,
    "SeriesType" integer NOT NULL,
    "Network" text,
    "UseSceneNumbering" boolean NOT NULL,
    "FirstAired" timestamp with time zone,
    "NextAiring" timestamp with time zone,
    "Year" integer,
    "Seasons" text,
    "Actors" text,
    "Ratings" text,
    "Genres" text,
    "Certification" text,
    "SortTitle" text,
    "QualityProfileId" integer,
    "Tags" text,
    "Added" timestamp with time zone,
    "AddOptions" text,
    "TvMazeId" integer DEFAULT 0 NOT NULL,
    "OriginalLanguage" integer DEFAULT 1 NOT NULL,
    "LastAired" timestamp with time zone,
    "MonitorNewItems" integer DEFAULT 0 NOT NULL,
    "TmdbId" integer DEFAULT 0 NOT NULL,
    "MalIds" text DEFAULT '[]'::text NOT NULL,
    "AniListIds" text DEFAULT '[]'::text NOT NULL,
    "OriginalCountry" text
);


--
-- Name: Series_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."Series" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."Series_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: SubtitleFiles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."SubtitleFiles" (
    "Id" integer NOT NULL,
    "SeriesId" integer NOT NULL,
    "SeasonNumber" integer NOT NULL,
    "EpisodeFileId" integer NOT NULL,
    "RelativePath" text NOT NULL,
    "Extension" text NOT NULL,
    "Added" timestamp with time zone,
    "LastUpdated" timestamp with time zone NOT NULL,
    "Language" integer NOT NULL,
    "LanguageTags" text,
    "Title" text,
    "Copy" integer DEFAULT 0 NOT NULL
);


--
-- Name: SubtitleFiles_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."SubtitleFiles" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."SubtitleFiles_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: Tags; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."Tags" (
    "Id" integer NOT NULL,
    "Label" text NOT NULL
);


--
-- Name: Tags_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."Tags" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."Tags_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: Users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."Users" (
    "Id" integer NOT NULL,
    "Identifier" text NOT NULL,
    "Username" text NOT NULL,
    "Password" text NOT NULL,
    "Salt" text,
    "Iterations" integer
);


--
-- Name: Users_Id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public."Users" ALTER COLUMN "Id" ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public."Users_Id_seq"
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: VersionInfo; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."VersionInfo" (
    "Version" bigint NOT NULL,
    "AppliedOn" timestamp with time zone,
    "Description" character varying(1024)
);


--
-- Name: AutoTagging PK_AutoTagging; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."AutoTagging"
    ADD CONSTRAINT "PK_AutoTagging" PRIMARY KEY ("Id");


--
-- Name: Blocklist PK_Blacklist; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."Blocklist"
    ADD CONSTRAINT "PK_Blacklist" PRIMARY KEY ("Id");


--
-- Name: Commands PK_Commands; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."Commands"
    ADD CONSTRAINT "PK_Commands" PRIMARY KEY ("Id");


--
-- Name: Config PK_Config; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."Config"
    ADD CONSTRAINT "PK_Config" PRIMARY KEY ("Id");


--
-- Name: CustomFilters PK_CustomFilters; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."CustomFilters"
    ADD CONSTRAINT "PK_CustomFilters" PRIMARY KEY ("Id");


--
-- Name: CustomFormats PK_CustomFormats; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."CustomFormats"
    ADD CONSTRAINT "PK_CustomFormats" PRIMARY KEY ("Id");


--
-- Name: DelayProfiles PK_DelayProfiles; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."DelayProfiles"
    ADD CONSTRAINT "PK_DelayProfiles" PRIMARY KEY ("Id");


--
-- Name: DownloadClientStatus PK_DownloadClientStatus; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."DownloadClientStatus"
    ADD CONSTRAINT "PK_DownloadClientStatus" PRIMARY KEY ("Id");


--
-- Name: DownloadClients PK_DownloadClients; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."DownloadClients"
    ADD CONSTRAINT "PK_DownloadClients" PRIMARY KEY ("Id");


--
-- Name: DownloadHistory PK_DownloadHistory; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."DownloadHistory"
    ADD CONSTRAINT "PK_DownloadHistory" PRIMARY KEY ("Id");


--
-- Name: EpisodeFiles PK_EpisodeFiles; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."EpisodeFiles"
    ADD CONSTRAINT "PK_EpisodeFiles" PRIMARY KEY ("Id");


--
-- Name: Episodes PK_Episodes; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."Episodes"
    ADD CONSTRAINT "PK_Episodes" PRIMARY KEY ("Id");


--
-- Name: ExtraFiles PK_ExtraFiles; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."ExtraFiles"
    ADD CONSTRAINT "PK_ExtraFiles" PRIMARY KEY ("Id");


--
-- Name: History PK_History; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."History"
    ADD CONSTRAINT "PK_History" PRIMARY KEY ("Id");


--
-- Name: ImportListExclusions PK_ImportListExclusions; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."ImportListExclusions"
    ADD CONSTRAINT "PK_ImportListExclusions" PRIMARY KEY ("Id");


--
-- Name: ImportListItems PK_ImportListItems; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."ImportListItems"
    ADD CONSTRAINT "PK_ImportListItems" PRIMARY KEY ("Id");


--
-- Name: ImportListStatus PK_ImportListStatus; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."ImportListStatus"
    ADD CONSTRAINT "PK_ImportListStatus" PRIMARY KEY ("Id");


--
-- Name: ImportLists PK_ImportLists; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."ImportLists"
    ADD CONSTRAINT "PK_ImportLists" PRIMARY KEY ("Id");


--
-- Name: IndexerStatus PK_IndexerStatus; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."IndexerStatus"
    ADD CONSTRAINT "PK_IndexerStatus" PRIMARY KEY ("Id");


--
-- Name: Indexers PK_Indexers; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."Indexers"
    ADD CONSTRAINT "PK_Indexers" PRIMARY KEY ("Id");


--
-- Name: Metadata PK_Metadata; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."Metadata"
    ADD CONSTRAINT "PK_Metadata" PRIMARY KEY ("Id");


--
-- Name: MetadataFiles PK_MetadataFiles; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."MetadataFiles"
    ADD CONSTRAINT "PK_MetadataFiles" PRIMARY KEY ("Id");


--
-- Name: NamingConfig PK_NamingConfig; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."NamingConfig"
    ADD CONSTRAINT "PK_NamingConfig" PRIMARY KEY ("Id");


--
-- Name: NotificationStatus PK_NotificationStatus; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."NotificationStatus"
    ADD CONSTRAINT "PK_NotificationStatus" PRIMARY KEY ("Id");


--
-- Name: Notifications PK_Notifications; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."Notifications"
    ADD CONSTRAINT "PK_Notifications" PRIMARY KEY ("Id");


--
-- Name: PendingReleases PK_PendingReleases; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."PendingReleases"
    ADD CONSTRAINT "PK_PendingReleases" PRIMARY KEY ("Id");


--
-- Name: QualityDefinitions PK_QualityDefinitions; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."QualityDefinitions"
    ADD CONSTRAINT "PK_QualityDefinitions" PRIMARY KEY ("Id");


--
-- Name: QualityProfiles PK_QualityProfiles; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."QualityProfiles"
    ADD CONSTRAINT "PK_QualityProfiles" PRIMARY KEY ("Id");


--
-- Name: RemotePathMappings PK_RemotePathMappings; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."RemotePathMappings"
    ADD CONSTRAINT "PK_RemotePathMappings" PRIMARY KEY ("Id");


--
-- Name: ReleaseProfiles PK_Restrictions; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."ReleaseProfiles"
    ADD CONSTRAINT "PK_Restrictions" PRIMARY KEY ("Id");


--
-- Name: RootFolders PK_RootFolders; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."RootFolders"
    ADD CONSTRAINT "PK_RootFolders" PRIMARY KEY ("Id");


--
-- Name: SceneMappings PK_SceneMappings; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."SceneMappings"
    ADD CONSTRAINT "PK_SceneMappings" PRIMARY KEY ("Id");


--
-- Name: ScheduledTasks PK_ScheduledTasks; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."ScheduledTasks"
    ADD CONSTRAINT "PK_ScheduledTasks" PRIMARY KEY ("Id");


--
-- Name: Series PK_Series; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."Series"
    ADD CONSTRAINT "PK_Series" PRIMARY KEY ("Id");


--
-- Name: SubtitleFiles PK_SubtitleFiles; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."SubtitleFiles"
    ADD CONSTRAINT "PK_SubtitleFiles" PRIMARY KEY ("Id");


--
-- Name: Tags PK_Tags; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."Tags"
    ADD CONSTRAINT "PK_Tags" PRIMARY KEY ("Id");


--
-- Name: Users PK_Users; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."Users"
    ADD CONSTRAINT "PK_Users" PRIMARY KEY ("Id");


--
-- Name: IX_AutoTagging_Name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_AutoTagging_Name" ON public."AutoTagging" USING btree ("Name");


--
-- Name: IX_Blacklist_SeriesId; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_Blacklist_SeriesId" ON public."Blocklist" USING btree ("SeriesId");


--
-- Name: IX_Config_Key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_Config_Key" ON public."Config" USING btree ("Key");


--
-- Name: IX_CustomFormats_Name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_CustomFormats_Name" ON public."CustomFormats" USING btree ("Name");


--
-- Name: IX_DownloadClientStatus_ProviderId; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_DownloadClientStatus_ProviderId" ON public."DownloadClientStatus" USING btree ("ProviderId");


--
-- Name: IX_DownloadHistory_DownloadId; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_DownloadHistory_DownloadId" ON public."DownloadHistory" USING btree ("DownloadId");


--
-- Name: IX_DownloadHistory_EventType; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_DownloadHistory_EventType" ON public."DownloadHistory" USING btree ("EventType");


--
-- Name: IX_DownloadHistory_SeriesId; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_DownloadHistory_SeriesId" ON public."DownloadHistory" USING btree ("SeriesId");


--
-- Name: IX_EpisodeFiles_SeriesId; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_EpisodeFiles_SeriesId" ON public."EpisodeFiles" USING btree ("SeriesId");


--
-- Name: IX_Episodes_EpisodeFileId; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_Episodes_EpisodeFileId" ON public."Episodes" USING btree ("EpisodeFileId");


--
-- Name: IX_Episodes_SeriesId; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_Episodes_SeriesId" ON public."Episodes" USING btree ("SeriesId");


--
-- Name: IX_Episodes_SeriesId_AirDate; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_Episodes_SeriesId_AirDate" ON public."Episodes" USING btree ("SeriesId", "AirDate");


--
-- Name: IX_Episodes_SeriesId_SeasonNumber_EpisodeNumber; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_Episodes_SeriesId_SeasonNumber_EpisodeNumber" ON public."Episodes" USING btree ("SeriesId", "SeasonNumber", "EpisodeNumber");


--
-- Name: IX_History_Date; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_History_Date" ON public."History" USING btree ("Date");


--
-- Name: IX_History_DownloadId_Date; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_History_DownloadId_Date" ON public."History" USING btree ("DownloadId", "Date" DESC);


--
-- Name: IX_History_EpisodeId_Date; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_History_EpisodeId_Date" ON public."History" USING btree ("EpisodeId", "Date" DESC);


--
-- Name: IX_History_EventType; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_History_EventType" ON public."History" USING btree ("EventType");


--
-- Name: IX_History_SeriesId; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_History_SeriesId" ON public."History" USING btree ("SeriesId");


--
-- Name: IX_ImportListExclusions_TvdbId; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_ImportListExclusions_TvdbId" ON public."ImportListExclusions" USING btree ("TvdbId");


--
-- Name: IX_ImportListStatus_ProviderId; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_ImportListStatus_ProviderId" ON public."ImportListStatus" USING btree ("ProviderId");


--
-- Name: IX_ImportLists_Name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_ImportLists_Name" ON public."ImportLists" USING btree ("Name");


--
-- Name: IX_IndexerStatus_IndexerId; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_IndexerStatus_IndexerId" ON public."IndexerStatus" USING btree ("ProviderId");


--
-- Name: IX_Indexers_Name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_Indexers_Name" ON public."Indexers" USING btree ("Name");


--
-- Name: IX_NotificationStatus_ProviderId; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_NotificationStatus_ProviderId" ON public."NotificationStatus" USING btree ("ProviderId");


--
-- Name: IX_QualityDefinitions_Quality; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_QualityDefinitions_Quality" ON public."QualityDefinitions" USING btree ("Quality");


--
-- Name: IX_QualityDefinitions_Title; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_QualityDefinitions_Title" ON public."QualityDefinitions" USING btree ("Title");


--
-- Name: IX_QualityProfiles_Name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_QualityProfiles_Name" ON public."QualityProfiles" USING btree ("Name");


--
-- Name: IX_RootFolders_Path; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_RootFolders_Path" ON public."RootFolders" USING btree ("Path");


--
-- Name: IX_ScheduledTasks_TypeName; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_ScheduledTasks_TypeName" ON public."ScheduledTasks" USING btree ("TypeName");


--
-- Name: IX_Series_CleanTitle; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_Series_CleanTitle" ON public."Series" USING btree ("CleanTitle");


--
-- Name: IX_Series_Path; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_Series_Path" ON public."Series" USING btree ("Path");


--
-- Name: IX_Series_TitleSlug; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_Series_TitleSlug" ON public."Series" USING btree ("TitleSlug");


--
-- Name: IX_Series_TmdbId; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_Series_TmdbId" ON public."Series" USING btree ("TmdbId");


--
-- Name: IX_Series_TvMazeId; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_Series_TvMazeId" ON public."Series" USING btree ("TvMazeId");


--
-- Name: IX_Series_TvRageId; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX "IX_Series_TvRageId" ON public."Series" USING btree ("TvRageId");


--
-- Name: IX_Series_TvdbId; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_Series_TvdbId" ON public."Series" USING btree ("TvdbId");


--
-- Name: IX_Tags_Label; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_Tags_Label" ON public."Tags" USING btree ("Label");


--
-- Name: IX_Users_Identifier; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_Users_Identifier" ON public."Users" USING btree ("Identifier");


--
-- Name: IX_Users_Username; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "IX_Users_Username" ON public."Users" USING btree ("Username");


--
-- Name: UC_Version; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX "UC_Version" ON public."VersionInfo" USING btree ("Version");


--
-- PostgreSQL database dump complete
--

\unrestrict 0uKplV1LSsgGBpi3yFNvFyoJAQVgTpZVrDXUqhxJuiwkbg3iZtbPgmbwvzvwNEc

