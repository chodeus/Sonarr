package v3

import (
	"encoding/json"
	"net/http"
	"sort"

	"sonarr.tv/sonarr/internal/healthcheck"
)

// HealthResource is the JSON shape produced by GET /api/v3/health.
// Mirrors `src/Sonarr.Api.V3/Health/HealthResource.cs`. Each entry
// describes one currently-failing health check.
//
// Field-source mapping (C# → Go):
//
//	id      → HealthCheck.Id (omitempty — passing checks aren't
//	          emitted, and the in-memory cache assigns no Ids today
//	          so the field is always 0 / omitted)
//	source  → HealthCheck.Source (the check's struct name —
//	          "IndexerRssCheck", "DownloadClientCheck", etc.)
//	type    → HealthCheck.Type (Result enum → camelCase string —
//	          "error" / "warning" / "notice" / "ok")
//	message → HealthCheck.Message (localized human-readable text)
//	wikiUrl → HealthCheck.WikiURL (`wikiBase` + `#anchor` derived
//	          from message via the `cleanFragmentRegex` pipeline)
type HealthResource struct {
	ID      int    `json:"id,omitempty"`
	Source  string `json:"source"`
	Type    string `json:"type"`
	Message string `json:"message"`
	WikiURL string `json:"wikiUrl"`
}

// HealthResultsLister narrows the healthcheck.Service surface to the
// failing-check Results() snapshot. *healthcheck.Service satisfies
// it directly. The handler doesn't trigger a run — Results() is the
// cached projection the orchestrator maintains via startup + event-
// driven + scheduled batches.
type HealthResultsLister interface {
	Results() []healthcheck.HealthCheck
}

// HealthHandler returns the http.Handler for GET /api/v3/health.
// Mirrors `HealthController.GetHealth` at
// `src/Sonarr.Api.V3/Health/HealthController.cs:39-44`. The C# path
// returns `_healthCheckService.Results().ToResource()` whose order
// depends on the underlying `Cached<HealthCheck>` insertion order
// (.NET Dictionary preserves insertion order, so the C# wire-order
// reflects the C# DI assembly-scan order — which is NOT alphabetical).
//
// The Go orchestrator's Results() iterates a map (non-deterministic
// order), so the handler imposes a deterministic sort to match the
// recorded golden:
//
//   - PRIMARY: Result type DESC (Error > Warning > Notice > OK) —
//     drives the most-severe-first ordering the recording captures.
//   - SECONDARY: Source ASC (alphabetical by check name) —
//     deterministic tiebreaker.
//
// This sort happens to reproduce the recording's exact emit order
// (IndexerRssCheck/error → DownloadClientCheck/warning →
// IndexerSearchCheck/warning) for the empty-DB recording. If a
// future recording captures a different order from C#, this sort
// becomes the explicit Go contract; the byte-eq test will flag any
// drift.
func HealthHandler(lister HealthResultsLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		results := lister.Results()
		sort.SliceStable(results, func(i, j int) bool {
			if results[i].Type != results[j].Type {
				return results[i].Type > results[j].Type
			}
			return results[i].Source < results[j].Source
		})
		out := make([]HealthResource, 0, len(results))
		for _, hc := range results {
			out = append(out, HealthResource{
				Source:  hc.Source,
				Type:    hc.Type.String(),
				Message: hc.Message,
				WikiURL: hc.WikiURL,
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}
