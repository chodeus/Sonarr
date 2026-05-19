package healthcheck

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

// IndexerDownloadClientCheckName is the canonical Source identifier.
const IndexerDownloadClientCheckName = "IndexerDownloadClientCheck"

// IndexerDCLister narrows `*indexers.Repo` to the read surface the
// check uses. *indexers.Repo satisfies it via Repository[Indexer].All.
type IndexerDCLister interface {
	All(ctx context.Context) ([]indexers.Indexer, error)
}

// DownloadClientRowLister narrows `*downloadclients.Repo` to the
// .All read surface the check uses. *downloadclients.Repo satisfies
// it via Repository[DownloadClient].All. Named distinctly from the
// pre-existing `DownloadClientRowLister` (which counts enabled clients
// for DownloadClientCheck) to avoid the type-name collision; both
// interfaces exist because the two checks have different access
// shapes — count vs full-row enumeration.
type DownloadClientRowLister interface {
	All(ctx context.Context) ([]downloadclients.DownloadClient, error)
}

// IndexerDownloadClientCheck warns when one or more enabled indexers
// pin a `DownloadClientId > 0` that does not match any enabled
// download client. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/IndexerDownloadClientCheck.cs:1-50`.
//
// The C# check's filter is:
//
//	indexer.Enable && indexer.DownloadClientId > 0 &&
//	  !enabledDC.Contains(indexer.DownloadClientId)
//
// "Enabled" on an indexer is the OR of EnableRss /
// EnableAutomaticSearch / EnableInteractiveSearch (per `IndexerDefinition.cs:35`).
// The Go port keeps the same filter; `indexerEnabled` is shared with
// IndexerJackettAllCheck.
type IndexerDownloadClientCheck struct {
	indexers IndexerDCLister
	clients  DownloadClientRowLister
}

// NewIndexerDownloadClientCheck builds the check.
func NewIndexerDownloadClientCheck(ix IndexerDCLister, dc DownloadClientRowLister) (*IndexerDownloadClientCheck, error) {
	if ix == nil {
		return nil, fmt.Errorf("healthcheck: NewIndexerDownloadClientCheck: nil IndexerDCLister")
	}
	if dc == nil {
		return nil, fmt.Errorf("healthcheck: NewIndexerDownloadClientCheck: nil DownloadClientRowLister")
	}
	return &IndexerDownloadClientCheck{indexers: ix, clients: dc}, nil
}

// Name returns the canonical check identifier.
func (*IndexerDownloadClientCheck) Name() string { return IndexerDownloadClientCheckName }

// CheckOnStartup defaults true.
func (*IndexerDownloadClientCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*IndexerDownloadClientCheck) CheckOnSchedule() bool { return true }

// EventTriggers mirrors the four C# `[CheckOn]` attributes at
// `IndexerDownloadClientCheck.cs:10-13`. C# re-runs on every indexer
// add/update/delete + every DC add/update/delete. The Go-port
// provider lifecycle events haven't ported yet — the closest
// available proxy is the configfile-saved event which fires when
// the v3 admin API persists an indexer or DC change. Indexer/DC
// provider events will slot in alongside their port.
func (*IndexerDownloadClientCheck) EventTriggers() []EventTrigger {
	return []EventTrigger{
		{EventType: reflect.TypeOf(configfile.ConfigFileSavedEvent{})},
	}
}

// Run mirrors `IndexerDownloadClientCheck.Check()` at `:28-48`.
func (c *IndexerDownloadClientCheck) Run(ctx context.Context) HealthCheck {
	dcRows, err := c.clients.All(ctx)
	if err != nil {
		return New(
			IndexerDownloadClientCheckName,
			ResultWarning,
			ReasonIndexerDownloadClient,
			fmt.Sprintf("Download client list lookup failed: %v", err),
			"#invalid-indexer-download-client-setting",
		)
	}
	enabledDC := make(map[int]struct{}, len(dcRows))
	for _, dc := range dcRows {
		if dc.Enable {
			enabledDC[dc.ID] = struct{}{}
		}
	}
	ixRows, err := c.indexers.All(ctx)
	if err != nil {
		return New(
			IndexerDownloadClientCheckName,
			ResultWarning,
			ReasonIndexerDownloadClient,
			fmt.Sprintf("Indexer list lookup failed: %v", err),
			"#invalid-indexer-download-client-setting",
		)
	}
	var bad []string
	for i := range ixRows {
		row := &ixRows[i]
		if !indexerEnabled(row) {
			continue
		}
		if row.DownloadClientID <= 0 {
			continue
		}
		if _, ok := enabledDC[row.DownloadClientID]; ok {
			continue
		}
		bad = append(bad, row.Name)
	}
	if len(bad) == 0 {
		return OK(IndexerDownloadClientCheckName)
	}
	sort.Strings(bad)
	return New(
		IndexerDownloadClientCheckName,
		ResultWarning,
		ReasonIndexerDownloadClient,
		"The following indexers reference a download client that is missing or disabled: "+strings.Join(bad, ", "),
		"#invalid-indexer-download-client-setting",
	)
}
