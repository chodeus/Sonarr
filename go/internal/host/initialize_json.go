// initialize.json endpoint — the React app's first fetch on boot.
// Ports C# `src/Sonarr.Http/Frontend/InitializeJsonController.cs:33-65`.
//
// The React entrypoint at `frontend/src/index.ts:5-8` does:
//
//   const response = await fetch(`${window.Sonarr.urlBase}/initialize.json`);
//   window.Sonarr = await response.json();
//
// before ANY other code runs. The shape returned here populates
// window.Sonarr globally and downstream API calls / SignalR connects
// read apiKey + apiRoot + urlBase from it. Missing this endpoint
// causes the React bundle to crash at response.json() (the SPA fallback
// serves the index.html, which is not valid JSON), so the UI never
// renders. J204 closes the gap surfaced by the frontend smoke.

package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"sonarr.tv/sonarr/internal/buildinfo"
)

// InitializeJSONConfig is the narrow slice of *configfile.Provider the
// handler reads. Defined as an interface so the host package stays
// decoupled from configfile (the cmd/sonarr boot wiring is the
// composition point that satisfies it).
//
// All getters take ctx because the underlying configfile.Provider is
// goroutine-safe and persistence-backed; the API key generator + URL
// base normaliser need a context for any I/O they trigger on first
// call (typically a no-op after boot).
type InitializeJSONConfig interface {
	APIKey(ctx context.Context) (string, error)
	URLBase(ctx context.Context) (string, error)
	InstanceName(ctx context.Context) (string, error)
	Branch(ctx context.Context) (string, error)
	Theme(ctx context.Context) (string, error)
}

// initializeJSONPayload mirrors the C# JSON shape line-for-line
// (`InitializeJsonController.GetContent` at `:42-58`). Field names
// preserve the C# camelCase casing the React app expects — verified
// against the consumer at `frontend/src/index.ts` (assigns the parsed
// JSON directly into window.Sonarr and the rest of the bundle reads
// `window.Sonarr.apiKey`, `.apiRoot`, etc.).
//
// Deferred (not yet wired):
//
//   - analytics: depends on the AnalyticsService port — defaults to
//     false so the UI doesn't try to talk to the analytics endpoint.
//   - userHash: anonymous-token hash; C# uses HashUtil.AnonymousToken()
//     which derives from MAC address + machine GUID. Go-port stamps a
//     stable SHA256 over the instance name so the user sees a
//     deterministic identifier without porting the machine-info dep
//     tree. Behavioural divergence — documented.
type initializeJSONPayload struct {
	APIRoot      string `json:"apiRoot"`
	APIKey       string `json:"apiKey"`
	Release      string `json:"release"`
	Version      string `json:"version"`
	InstanceName string `json:"instanceName"`
	Theme        string `json:"theme"`
	Branch       string `json:"branch"`
	Analytics    bool   `json:"analytics"`
	UserHash     string `json:"userHash"`
	URLBase      string `json:"urlBase"`
	IsProduction bool   `json:"isProduction"`
}

// InitializeJSONHandler builds the http.Handler that serves
// /initialize.json. The handler must be registered as Anonymous —
// the React bundle hits this BEFORE it has the API key, so any
// requireAPIKey wrap will hard-block the UI from booting.
//
// The C# version is gated behind `[Authorize(Policy = "UI")]` which
// requires a forms-auth session cookie when AuthenticationMethod is
// Forms / Basic. The Go-port defers that gate until the forms-auth
// middleware ports — for now (AuthenticationMethod=None default) the
// behaviour is identical.
func InitializeJSONHandler(cfg InitializeJSONConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		apiKey, err := cfg.APIKey(ctx)
		if err != nil {
			http.Error(w, "initialize.json: APIKey lookup failed", http.StatusInternalServerError)
			return
		}
		urlBase, err := cfg.URLBase(ctx)
		if err != nil {
			http.Error(w, "initialize.json: URLBase lookup failed", http.StatusInternalServerError)
			return
		}
		instanceName, err := cfg.InstanceName(ctx)
		if err != nil {
			http.Error(w, "initialize.json: InstanceName lookup failed", http.StatusInternalServerError)
			return
		}
		branch, err := cfg.Branch(ctx)
		if err != nil {
			http.Error(w, "initialize.json: Branch lookup failed", http.StatusInternalServerError)
			return
		}
		theme, err := cfg.Theme(ctx)
		if err != nil {
			http.Error(w, "initialize.json: Theme lookup failed", http.StatusInternalServerError)
			return
		}

		// userHash: stable per-instance SHA256 over the instance name.
		// C# uses MAC-derived AnonymousToken; the deterministic instance
		// hash is good enough for the UI's "this is your instance"
		// signal without the platform-info dep tree.
		h := sha256.Sum256([]byte(instanceName))
		userHash := hex.EncodeToString(h[:])

		// "release" mirrors C# `BuildInfo.Release` which is the
		// distribution channel ("main", "develop", "nightly"). The
		// Go-port reuses Branch for this — they're conceptually the
		// same channel identifier and C# does too in practice.
		payload := initializeJSONPayload{
			APIRoot:      urlBase + "/api/v3",
			APIKey:       apiKey,
			Release:      branch,
			Version:      buildinfo.Version,
			InstanceName: instanceName,
			Theme:        theme,
			Branch:       branch,
			Analytics:    false,
			UserHash:     userHash,
			URLBase:      urlBase,
			IsProduction: buildinfo.Version != "dev",
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(payload)
	})
}
