package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/indexers"
)

// IndexerJackettAllCheckName is the canonical Source identifier.
const IndexerJackettAllCheckName = "IndexerJackettAllCheck"

// jackettAllPathOld + jackettAllPathV2 are the two Jackett URL
// fragments that route to the all-indexers endpoint. Sonarr does
// not support that endpoint (per-indexer responses can't be
// classified back to the originating indexer), so any enabled
// indexer pointing at one warrants a warning. Mirrors the constants
// in `internal/indexers/torznab/torznab.go:123-124`.
const (
	jackettAllPathOld = "/torznab/all"
	jackettAllPathV2  = "/api/v2.0/indexers/all/results/torznab"
)

// IndexerJackettAllLister narrows `*indexers.Repo` to the read
// surface the check needs. `Repo.All(ctx)` already returns the
// fully-projected []Indexer rows including Settings + ConfigContract,
// so a wider interface isn't needed.
type IndexerJackettAllLister interface {
	All(ctx context.Context) ([]indexers.Indexer, error)
}

// IndexerJackettAllCheck warns when one or more enabled indexers
// point at Jackett's all-indexers endpoint. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/IndexerJackettAllCheck.cs:1-53`.
//
// The C# check filters to `i.Enable && i.ConfigContract == "TorznabSettings"`
// and inspects `(TorznabSettings).BaseUrl` / `.ApiPath` for the two
// jackett-all path fragments. The Go port keeps the same filter but
// unmarshals only the BaseUrl + ApiPath fields it needs (avoids a
// dependency on the full `internal/indexers/torznab` package — the
// check only needs the URL strings, not the validator surface).
type IndexerJackettAllCheck struct {
	lister IndexerJackettAllLister
}

// NewIndexerJackettAllCheck builds the check.
func NewIndexerJackettAllCheck(lister IndexerJackettAllLister) (*IndexerJackettAllCheck, error) {
	if lister == nil {
		return nil, fmt.Errorf("healthcheck: NewIndexerJackettAllCheck: nil IndexerJackettAllLister")
	}
	return &IndexerJackettAllCheck{lister: lister}, nil
}

// Name returns the canonical check identifier.
func (*IndexerJackettAllCheck) Name() string { return IndexerJackettAllCheckName }

// CheckOnStartup defaults true.
func (*IndexerJackettAllCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*IndexerJackettAllCheck) CheckOnSchedule() bool { return true }

// EventTriggers mirrors the C# `[CheckOn]` attributes at
// `IndexerJackettAllCheck.cs:12-15`. The C# class re-runs on every
// indexer add / update / delete / status-change. The Go-port
// equivalent events (provider lifecycle + indexer status) aren't all
// in place yet — the closest available is the configfile-saved event
// which fires when an indexer-list change is persisted via the v3
// admin API. Indexer-status events are tracked separately and will
// slot in alongside their port; until then the check still runs on
// startup + scheduled cadence, which covers most operational cases.
func (*IndexerJackettAllCheck) EventTriggers() []EventTrigger {
	return []EventTrigger{
		{EventType: reflect.TypeOf(configfile.ConfigFileSavedEvent{})},
	}
}

// Run mirrors `IndexerJackettAllCheck.Check()` at `:26-52`.
// Builds a sorted list of indexer names whose Torznab settings carry
// a jackett-all path fragment, then emits a single warning whose
// message names every such indexer.
func (c *IndexerJackettAllCheck) Run(ctx context.Context) HealthCheck {
	rows, err := c.lister.All(ctx)
	if err != nil {
		return New(
			IndexerJackettAllCheckName,
			ResultWarning,
			ReasonIndexerJackettAll,
			fmt.Sprintf("Indexer list lookup failed: %v", err),
			"#jackett-all-endpoint-used",
		)
	}
	var bad []string
	for i := range rows {
		row := &rows[i]
		if !indexerEnabled(row) {
			continue
		}
		if row.ConfigContract == nil || *row.ConfigContract != "TorznabSettings" {
			continue
		}
		if row.Settings == nil || *row.Settings == "" {
			continue
		}
		if hasJackettAllEndpoint(*row.Settings) {
			bad = append(bad, row.Name)
		}
	}
	if len(bad) == 0 {
		return OK(IndexerJackettAllCheckName)
	}
	sort.Strings(bad)
	return New(
		IndexerJackettAllCheckName,
		ResultWarning,
		ReasonIndexerJackettAll,
		"The following indexers use the unsupported Jackett all-indexers endpoint: "+strings.Join(bad, ", "),
		"#jackett-all-endpoint-used",
	)
}

// indexerEnabled mirrors C# `IndexerDefinition.Enable` at
// `IndexerDefinition.cs:35` — OR of the three enable flags. The Go
// `Indexer.EnableRss` / `EnableAutomaticSearch` are nullable (`*bool`,
// matching the schema columns); nil decays to false.
func indexerEnabled(row *indexers.Indexer) bool {
	rss := row.EnableRss != nil && *row.EnableRss
	auto := row.EnableAutomaticSearch != nil && *row.EnableAutomaticSearch
	return rss || auto || row.EnableInteractiveSearch
}

// jackettSettingsSubset decodes only the two fields the check
// inspects. Avoids importing the full torznab.Settings type (which
// would pull in newznab + provider + the validator surface) and
// keeps the JSON tolerance loose — unknown keys are ignored, which
// matches the C# `JsonSerializer.Deserialize<TorznabSettings>` shape
// after STJson's PropertyNameCaseInsensitive=true is applied.
type jackettSettingsSubset struct {
	BaseURL string `json:"baseUrl"`
	APIPath string `json:"apiPath"`
}

// hasJackettAllEndpoint returns true when the JSON settings blob
// contains a BaseUrl or ApiPath pointing at one of the two
// jackett-all URL forms. Case-insensitive on the path fragment to
// mirror the C# `StringComparison.InvariantCultureIgnoreCase` at
// `:31-34`.
func hasJackettAllEndpoint(settingsJSON string) bool {
	var s jackettSettingsSubset
	if err := json.Unmarshal([]byte(settingsJSON), &s); err != nil {
		return false
	}
	base := strings.ToLower(s.BaseURL)
	api := strings.ToLower(s.APIPath)
	return strings.Contains(base, jackettAllPathOld) ||
		strings.Contains(base, jackettAllPathV2) ||
		strings.Contains(api, jackettAllPathOld) ||
		strings.Contains(api, jackettAllPathV2)
}
