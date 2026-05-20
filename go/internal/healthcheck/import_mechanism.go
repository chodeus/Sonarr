package healthcheck

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"

	"sonarr.tv/sonarr/internal/configfile"
)

// ImportMechanismCheckName is the canonical Source identifier.
const ImportMechanismCheckName = "ImportMechanismCheck"

// ImportMechanismCheckConfig narrows the *config.Provider to the
// two methods the check consumes. *config.Provider satisfies it
// directly.
type ImportMechanismCheckConfig interface {
	IsDefined(key string) bool
	EnableCompletedDownloadHandling() bool
}

// EnabledDownloadClientInfo is the projection ImportMechanismCheck
// consumes — the Name + bound-status pair, where Status carries
// IsLocalhost + the implementation name. Reuses
// `EnumeratedDownloadClient.Status` indirectly; the check pulls
// rows via the existing `DownloadClientStatusEnumerator` interface.

// ImportMechanismCheck warns when:
//   - EnableCompletedDownloadHandling has never been explicitly set by
//     the user AND any enabled DC is remote (multi-computer warning),
//   - OR all enabled DCs are local + sabnzbd/nzbget (single-host
//     CDH-not-set warning),
//   - OR EnableCompletedDownloadHandling has been set to false
//     (handling-disabled warning).
//
// Mirrors `src/NzbDrone.Core/HealthCheck/Checks/ImportMechanismCheck.cs:1-103`.
//
// Audit fix B.5g.
type ImportMechanismCheck struct {
	cfg     ImportMechanismCheckConfig
	clients DownloadClientStatusEnumerator
	log     *slog.Logger
}

// NewImportMechanismCheck builds the check. cfg + clients MUST be
// non-nil. log defaults to slog.Default() when nil.
func NewImportMechanismCheck(cfg ImportMechanismCheckConfig, clients DownloadClientStatusEnumerator, log *slog.Logger) (*ImportMechanismCheck, error) {
	if cfg == nil {
		return nil, fmt.Errorf("healthcheck: NewImportMechanismCheck: nil ImportMechanismCheckConfig")
	}
	if clients == nil {
		return nil, fmt.Errorf("healthcheck: NewImportMechanismCheck: nil DownloadClientStatusEnumerator")
	}
	if log == nil {
		log = slog.Default()
	}
	return &ImportMechanismCheck{cfg: cfg, clients: clients, log: log}, nil
}

// Name returns the canonical check identifier.
func (*ImportMechanismCheck) Name() string { return ImportMechanismCheckName }

// CheckOnStartup defaults true.
func (*ImportMechanismCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*ImportMechanismCheck) CheckOnSchedule() bool { return true }

// EventTriggers mirrors the three C# `[CheckOn]` attributes at
// `ImportMechanismCheck.cs:14-16` — re-runs on DC change + config-
// saved events. The Go-port DC provider events haven't ported yet;
// closest proxy is the config-saved event.
func (*ImportMechanismCheck) EventTriggers() []EventTrigger {
	return []EventTrigger{
		{EventType: reflect.TypeOf(configfile.ConfigFileSavedEvent{})},
	}
}

// Run mirrors C# `Check()` at `:29-96`.
func (c *ImportMechanismCheck) Run(ctx context.Context) HealthCheck {
	clients, err := c.clients.EnabledDownloadClients(ctx)
	if err != nil {
		// C# catches Exception + returns OK ("assume the health is
		// okay and verify later"); Go matches by emitting OK on
		// total-enumeration failure.
		c.log.Debug("import mechanism check: skip on enumeration error",
			slog.Any("err", err),
		)
		return OK(ImportMechanismCheckName)
	}
	// Filter out per-row Err entries — they're equivalent to the C#
	// per-client try/catch arm.
	type statusEntry struct {
		Name   string
		Local  bool
		ImplLC string // lowercase implementation name; "" if unknown
	}
	var rows []statusEntry
	for _, dc := range clients {
		if dc.Err != nil || dc.Status == nil {
			continue
		}
		rows = append(rows, statusEntry{
			Name:   dc.Name,
			Local:  dc.Status.IsLocalhost,
			ImplLC: strings.ToLower(dc.Implementation),
		})
	}

	if !c.cfg.IsDefined("EnableCompletedDownloadHandling") {
		// Migration helper logic. If no clients exist, emit the
		// generic "consider enabling CDH" warning — matches the C#
		// fall-through where the .All predicates over an empty list
		// short-circuit to true (Linq All-on-empty is vacuously
		// true).
		allLocal := true
		for _, r := range rows {
			if !r.Local {
				allLocal = false
				break
			}
		}
		if !allLocal {
			return New(
				ImportMechanismCheckName,
				ResultWarning,
				ReasonImportMechanismEnableCompletedDownloadHandlingIfPossibleMultiComputer,
				"Sonarr's Completed Download Handling is not enabled; enabling it for multi-host setups requires care because the download client and Sonarr live on different hosts",
				"#completedfailed-download-handling",
			)
		}
		// All-local path: SAB-only / NZBGet-only special messages.
		if len(rows) > 0 && allMatch(rows, func(r statusEntry) bool { return r.ImplLC == "sabnzbd" }) {
			return New(
				ImportMechanismCheckName,
				ResultWarning,
				ReasonImportMechanismEnableCompletedDownloadHandlingIfPossible,
				"Sonarr's Completed Download Handling is not enabled; consider turning it on (Sabnzbd)",
				"#completedfailed-download-handling",
			)
		}
		if len(rows) > 0 && allMatch(rows, func(r statusEntry) bool { return r.ImplLC == "nzbget" }) {
			return New(
				ImportMechanismCheckName,
				ResultWarning,
				ReasonImportMechanismEnableCompletedDownloadHandlingIfPossible,
				"Sonarr's Completed Download Handling is not enabled; consider turning it on (Nzbget)",
				"#completedfailed-download-handling",
			)
		}
		return New(
			ImportMechanismCheckName,
			ResultWarning,
			ReasonImportMechanismEnableCompletedDownloadHandlingIfPossible,
			"Sonarr's Completed Download Handling is not enabled; consider turning it on",
			"#completedfailed-download-handling",
		)
	}

	if !c.cfg.EnableCompletedDownloadHandling() {
		return New(
			ImportMechanismCheckName,
			ResultWarning,
			ReasonImportMechanismHandlingDisabled,
			"Sonarr's Completed Download Handling is disabled — file imports will not run",
			"#completed-download-handling-is-disabled",
		)
	}

	return OK(ImportMechanismCheckName)
}

// allMatch returns true when EVERY element of `rows` satisfies the
// predicate. Mirrors C# `Linq.All(...)`. Returns true when the
// slice is empty (`All` on an empty source is vacuously true).
func allMatch[T any](rows []T, pred func(T) bool) bool {
	for _, r := range rows {
		if !pred(r) {
			return false
		}
	}
	return true
}
