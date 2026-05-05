// Package healthcheck is the Go port of NzbDrone.Core.HealthCheck.
//
// Surface delivered in Phase 3.29 (the slice that closes the 3.24
// CheckHealth-handler stub):
//
//   - Result + Reason enums + HealthCheck value type — port of
//     `src/NzbDrone.Core/HealthCheck/HealthCheck.cs:1-119` including
//     the wiki-fragment derivation regex.
//   - Check interface — port of `IProvideHealthCheck` at
//     `src/NzbDrone.Core/HealthCheck/IProvideHealthCheck.cs`. The C#
//     `[CheckOn(typeof(X))]` attribute is replaced by an optional
//     `EventDrivenCheck` interface that lets a check declare its
//     event triggers programmatically (Go has no equivalent of
//     attribute reflection over types).
//   - FailedEvent / RestoredEvent / CompleteEvent — domain events
//     fired into the messaging bus on result transitions.
//   - Service orchestrator — port of `HealthCheckService.cs:1-232`
//     with the C# IExecute<CheckHealthCommand> + IHandleAsync<IEvent>
//   - IHandleAsync<ApplicationStartedEvent> shapes collapsed onto
//     a small Go API: Run(ctx, Trigger), HandleApplicationStarted,
//     HandleAnyEvent, Results.
//   - Two concrete checks: AppDataLocationCheck and
//     ApiKeyValidationCheck. Both are dependency-light (config +
//     appfolder only) so they exercise the orchestrator end-to-end
//     without dragging in series/rootfolder/disk subsystems.
//
// What's deliberately deferred to subsequent slices:
//
//   - The remaining ~20 C# checks (RootFolderCheck, MountCheck,
//     UpdateCheck, ProxyCheck, IndexerLongTermStatusCheck, etc.).
//     Each will land alongside its dependent subsystem (e.g.
//     UpdateCheck waits on the update-check service; IndexerRssCheck
//     waits on the indexer subsystem). The orchestrator surface is
//     stable — adding a check is `NewXxxCheck(...)` + register.
//   - HTTP API for `/api/v3/health` — Phase 5 territory. The
//     orchestrator already exposes `Service.Results()` so the future
//     handler is a single-line wrapper.
//   - `/api/v3/health/{id}` per-result lookups — the C# tree assigns
//     ModelBase Ids; the Go port keys results by check name (the
//     Source field) so per-result access is a map lookup.
package healthcheck

import (
	"regexp"
	"strings"
)

// Result mirrors the C# `HealthCheckResult` enum at
// `HealthCheck.cs:48-54`. Values are stable and persisted in the
// SignalR payload + JSON API; renumbering breaks the v3 frontend.
type Result int

const (
	// ResultOK indicates a passing check — usually NOT stored; the
	// orchestrator removes the result from the cache on transition
	// from a non-OK result to ResultOK and publishes RestoredEvent.
	ResultOK Result = 0
	// ResultNotice is informational. Surfaced as a banner; not blocking.
	ResultNotice Result = 1
	// ResultWarning is a non-fatal failure. Surfaced as a yellow banner.
	ResultWarning Result = 2
	// ResultError is a fatal failure. Surfaced as a red banner; some
	// checks (RootFolderMissing) prevent automatic activity until cleared.
	ResultError Result = 3
)

// String returns the lower-case name used in log lines + the v3 JSON
// payload (the C# `JsonConverter` lower-cases enum names).
func (r Result) String() string {
	switch r {
	case ResultOK:
		return "ok"
	case ResultNotice:
		return "notice"
	case ResultWarning:
		return "warning"
	case ResultError:
		return "error"
	}
	return "unknown"
}

// Reason mirrors the C# `HealthCheckReason` enum at
// `HealthCheck.cs:56-119`. Order + names ARE NOT persisted in any
// Sonarr-managed state today (the C# tree serialises only the int
// value into in-memory caches), but they ARE part of the v3 JSON
// API surface, so the order MUST match the C# declaration so that
// `int(reason)` produces the same wire value.
//
// To keep parity: when a new Reason appears in the C# tree, it MUST
// be appended to the END of this list (mirroring the C# practice of
// declaring new enum members at the bottom). Re-ordering here would
// silently shift every downstream wire value.
type Reason int

// Reason enumeration. Order is significant — see the Reason type
// docstring for the wire-stability contract. Each constant maps 1:1
// to a C# `HealthCheckReason` enum entry; the Go names mirror the
// C# names with a leading `Reason` qualifier.
const (
	ReasonAppDataLocation Reason = iota
	ReasonDownloadClientCheckNoneAvailable
	ReasonDownloadClientCheckUnableToCommunicate
	ReasonDownloadClientRemovesCompletedDownloads
	ReasonDownloadClientRootFolder
	ReasonDownloadClientSorting
	ReasonDownloadClientStatusAllClients
	ReasonDownloadClientStatusSingleClient
	ReasonImportListRootFolderMissing
	ReasonImportListRootFolderMultipleMissing
	ReasonImportListStatusAllUnavailable
	ReasonImportListStatusUnavailable
	ReasonImportMechanismEnableCompletedDownloadHandlingIfPossible
	ReasonImportMechanismEnableCompletedDownloadHandlingIfPossibleMultiComputer
	ReasonImportMechanismHandlingDisabled
	ReasonIndexerDownloadClient
	ReasonIndexerJackettAll
	ReasonIndexerLongTermStatusAllUnavailable
	ReasonIndexerLongTermStatusUnavailable
	ReasonIndexerRssNoIndexersAvailable
	ReasonIndexerRssNoIndexersEnabled
	ReasonIndexerSearchNoAutomatic
	ReasonIndexerSearchNoAvailableIndexers
	ReasonIndexerSearchNoInteractive
	ReasonIndexerStatusAllUnavailable
	ReasonIndexerStatusUnavailable
	ReasonMinimumApiKeyLength
	ReasonMountSeries
	ReasonNotificationStatusAll
	ReasonNotificationStatusSingle
	ReasonPackage
	ReasonProxyBadRequest
	ReasonProxyFailed
	ReasonProxyResolveIp
	ReasonRecycleBinUnableToWrite
	ReasonRemotePathMappingBadDockerPath
	ReasonRemotePathMappingDockerFolderMissing
	ReasonRemotePathMappingDownloadPermissionsEpisode
	ReasonRemotePathMappingFileRemoved
	ReasonRemotePathMappingFilesBadDockerPath
	ReasonRemotePathMappingFilesGenericPermissions
	ReasonRemotePathMappingFilesLocalWrongOSPath
	ReasonRemotePathMappingFilesWrongOSPath
	ReasonRemotePathMappingFolderPermissions
	ReasonRemotePathMappingGenericPermissions
	ReasonRemotePathMappingImportEpisodeFailed
	ReasonRemotePathMappingLocalFolderMissing
	ReasonRemotePathMappingLocalWrongOSPath
	ReasonRemotePathMappingRemoteDownloadClient
	ReasonRemotePathMappingWrongOSPath
	ReasonRemovedSeriesMultiple
	ReasonRemovedSeriesSingle
	ReasonRootFolderEmpty
	ReasonRootFolderMissing
	ReasonRootFolderMultipleMissing
	ReasonServerNotification
	ReasonSystemTime
	ReasonUpdateAvailable
	ReasonUpdateStartupNotWritable
	ReasonUpdateStartupTranslocation
	ReasonUpdateUiNotWritable
)

// HealthCheck is the value type a Check returns. Mirrors the C#
// ModelBase-derived class at `HealthCheck.cs:8-46`. Source is the
// check's struct name (the C# tree uses the .NET Type — Go takes
// the closest equivalent: a stable string injected by the check).
type HealthCheck struct {
	// Source is the check's identifier — used as the cache key and
	// emitted on the JSON API. Mirrors `Source.Name` in the C#
	// `_healthCheckResults.Set(result.Source.Name, result)`.
	Source string
	// Type is the check's outcome.
	Type Result
	// Reason categorises the failure for the v3 API. Zero value
	// (ReasonAppDataLocation) is meaningless on a passing check;
	// readers should gate Reason access on Type != ResultOK.
	Reason Reason
	// Message is a human-readable description, typically already
	// localised. Empty on passing checks.
	Message string
	// WikiURL is the wiki anchor for remediation steps. Empty on
	// passing checks. Format: https://wiki.servarr.com/sonarr/system#fragment
	WikiURL string
}

// New builds a HealthCheck. wikiFragment is optional — when empty,
// the fragment is derived from message via `cleanFragment`. Pass an
// explicit fragment when the message is parameterised (paths, names)
// so the wiki URL stays stable across users.
//
// Mirrors the C# constructor at `HealthCheck.cs:28-35`.
func New(source string, t Result, reason Reason, message, wikiFragment string) HealthCheck {
	hc := HealthCheck{
		Source:  source,
		Type:    t,
		Reason:  reason,
		Message: message,
	}
	if t != ResultOK {
		hc.WikiURL = makeWikiURL(message, wikiFragment)
	}
	return hc
}

// OK constructs a passing HealthCheck for the given source. Mirrors
// the C# `new HealthCheck(typeof(X))` constructor at `:22-26`.
func OK(source string) HealthCheck {
	return HealthCheck{Source: source, Type: ResultOK}
}

// wikiBase is the wiki URL prefix. Mirrors the C# `:43` constant.
const wikiBase = "https://wiki.servarr.com/sonarr/system"

// cleanFragmentRegex matches every character that isn't a lower-case
// ASCII letter or a space. Mirrors the C# `:10` regex
// `[^a-z ]`. Used to derive a wiki fragment from the message.
var cleanFragmentRegex = regexp.MustCompile(`[^a-z ]`)

// makeWikiURL implements the C# `MakeWikiUrl` + `MakeWikiFragment`
// pipeline at `:37-45`. fragment may be:
//
//   - "" — derive from message: lower-case, strip non-letter/non-space,
//     replace spaces with hyphens, prepend "#".
//   - "#xxx" — used verbatim, appended to the system page.
//   - "page#xxx" — replaces the system page with the named page.
//
// The third form is used by checks that point at a non-system wiki
// page (e.g. AppDataLocationCheck → "#updating-will-not-be-possible-...").
func makeWikiURL(message, fragment string) string {
	if fragment == "" {
		fragment = "#" + strings.ReplaceAll(
			cleanFragmentRegex.ReplaceAllString(strings.ToLower(message), ""),
			" ", "-")
	}
	// If the fragment carries a path component (`page#anchor`), it
	// REPLACES the system suffix in the base URL — we strip the
	// base's trailing path segment and append the fragment.
	if i := strings.IndexByte(fragment, '#'); i > 0 {
		// Has a custom page prefix.
		base := strings.TrimSuffix(wikiBase, "/system")
		return base + "/" + fragment
	}
	return wikiBase + fragment
}
