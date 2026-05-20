package tribler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// HTTPDoer is the [*http.Client] slice the [Proxy] depends on.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Proxy is the HTTP client for a Tribler endpoint. Mirrors C#
// `TriblerDownloadClientProxy` at `:19-118`. Stateless beyond the
// HTTP doer + logger — every call sends the static X-Api-Key
// header rather than maintaining a session.
type Proxy struct {
	http HTTPDoer
	log  *slog.Logger
}

// NewProxy constructs a Proxy. http=nil binds [http.DefaultClient];
// log=nil binds [slog.Default].
func NewProxy(httpDoer HTTPDoer, log *slog.Logger) *Proxy {
	if httpDoer == nil {
		httpDoer = http.DefaultClient
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{http: httpDoer, log: log}
}

// baseURL composes the absolute base. Mirrors C# `GetRequestBuilder`
// at `:30-42` — `<scheme>://<host>:<port><urlBase>`.
func baseURL(s Settings) string {
	scheme := "http"
	if s.UseSsl {
		scheme = "https"
	}
	ub := strings.TrimSpace(s.UrlBase)
	if ub != "" && !strings.HasPrefix(ub, "/") {
		ub = "/" + ub
	}
	return fmt.Sprintf("%s://%s:%d%s", scheme, s.Host, s.Port, strings.TrimRight(ub, "/"))
}

// Config ports the subset of C# `TriblerSettingsResponse` consumed
// by the Provider. Tribler's `/api/settings` payload nests both the
// save-as directory AND the seeding-policy knobs the cleanup-gate
// reads under `settings.libtorrent.download_defaults`.
//
// J96 extends the struct to surface SeedingMode/SeedingRatio/SeedingTime —
// the per-client seeding policy [TriblerDownloadClient.applyCleanupGate]
// consults. (Tribler diverges from RQBit/Flood: it does NOT consult
// the per-grab IDownloadSeedConfigProvider; the seeding gate is
// driven entirely by Tribler's own LibTorrent config.)
type Config struct {
	Settings struct {
		LibTorrent struct {
			DownloadDefaults struct {
				SaveAs       string       `json:"saveas"`
				SeedingMode  *SeedingMode `json:"seeding_mode"`
				SeedingRatio *float64     `json:"seeding_ratio"`
				SeedingTime  *float64     `json:"seeding_time"`
			} `json:"download_defaults"`
		} `json:"libtorrent"`
	} `json:"settings"`
}

// SeedingMode ports the C# `DownloadDefaultsSeedingMode` enum at
// `Models/TriblerSettingsApi.cs:175-188`. Tribler's wire format is a
// lowercase string ("ratio", "forever", "time", "never") — UnmarshalJSON
// maps each to its ordinal so call sites can switch on a typed
// constant rather than a magic string. Unknown strings unmarshal as
// SeedingModeUnknown so the cleanup-gate's default arm (matches C#
// `:215-217` `Forever / default`) fails safe (returns false).
type SeedingMode int

// SeedingMode ordinals. Order matches C# enum at `:175-188` so the
// numeric mapping stays stable for any future int-payload variant.
const (
	// SeedingModeRatio - "ratio" - C# `:177`. Gate fires when
	// AllTimeRatio >= SeedingRatio.
	SeedingModeRatio SeedingMode = 0
	// SeedingModeForever - "forever" - C# `:180`. Gate never fires;
	// torrent stays seeding indefinitely.
	SeedingModeForever SeedingMode = 1
	// SeedingModeTime - "time" - C# `:183`. Gate fires when
	// now - epoch(TimeAdded) >= SeedingTime seconds.
	SeedingModeTime SeedingMode = 2
	// SeedingModeNever - "never" - C# `:186`. Gate always fires the
	// moment the torrent reaches Stopped state.
	SeedingModeNever SeedingMode = 3

	// SeedingModeUnknown is the Go-port sentinel for an unrecognised
	// wire string. Treated identically to Forever (gate never fires).
	SeedingModeUnknown SeedingMode = -1
)

// UnmarshalJSON decodes the lowercase string wire format. Empty /
// null payload → SeedingModeUnknown (the C# nullable
// `DownloadDefaultsSeedingMode?` evaluates null in the switch's
// default arm, which returns false).
func (m *SeedingMode) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	switch strings.ToLower(s) {
	case "ratio":
		*m = SeedingModeRatio
	case "forever":
		*m = SeedingModeForever
	case "time":
		*m = SeedingModeTime
	case "never":
		*m = SeedingModeNever
	default:
		*m = SeedingModeUnknown
	}
	return nil
}

// DownloadStatus ports C# `Models/TriblerDownloadClientApi.cs:9-45`
// — the snake-or-UPPERCASE-tagged enum the Tribler API emits for
// the per-task state. The Go-port enum is a typed int with the
// same numeric ordering for in-package comparisons; the wire
// shape is a string handled by the custom UnmarshalJSON.
type DownloadStatus int

// DownloadStatus values mirror the C# enum at `:11-43` verbatim.
const (
	DownloadStatusWaiting4HashCheck   DownloadStatus = 0
	DownloadStatusHashchecking        DownloadStatus = 1
	DownloadStatusMetadata            DownloadStatus = 2
	DownloadStatusDownloading         DownloadStatus = 3
	DownloadStatusSeeding             DownloadStatus = 4
	DownloadStatusStopped             DownloadStatus = 5
	DownloadStatusAllocatingDiskspace DownloadStatus = 6
	DownloadStatusExitnodes           DownloadStatus = 7
	DownloadStatusCircuits            DownloadStatus = 8
	DownloadStatusStoppedOnError      DownloadStatus = 9
	DownloadStatusLoading             DownloadStatus = 10
)

// downloadStatusByWire maps the [EnumMember(Value=...)] strings
// from C# `:11-43` to the typed enum. Unknown values map to
// DownloadStatusLoading (matches C# default-arm fallback at
// `TriblerDownloadClient.cs:130-134`).
var downloadStatusByWire = map[string]DownloadStatus{
	"WAITING4HASHCHECK":    DownloadStatusWaiting4HashCheck,
	"HASHCHECKING":         DownloadStatusHashchecking,
	"METADATA":             DownloadStatusMetadata,
	"DOWNLOADING":          DownloadStatusDownloading,
	"SEEDING":              DownloadStatusSeeding,
	"STOPPED":              DownloadStatusStopped,
	"ALLOCATING_DISKSPACE": DownloadStatusAllocatingDiskspace,
	"EXIT_NODES":           DownloadStatusExitnodes,
	"CIRCUITS":             DownloadStatusCircuits,
	"STOPPED_ON_ERROR":     DownloadStatusStoppedOnError,
	"LOADING":              DownloadStatusLoading,
}

// UnmarshalJSON decodes the UPPER-snake-case wire string into the
// typed enum.
func (s *DownloadStatus) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		// Tolerate numeric form just in case.
		var n int
		if err2 := json.Unmarshal(data, &n); err2 == nil {
			*s = DownloadStatus(n)
			return nil
		}
		return err
	}
	if v, ok := downloadStatusByWire[strings.ToUpper(strings.TrimSpace(raw))]; ok {
		*s = v
		return nil
	}
	*s = DownloadStatusLoading
	return nil
}

// Download ports the subset of C# `Models/TriblerDownloadClientApi.cs:57-86`
// the Provider GetItems consumes. Pointer-types match the C#
// nullable shape — Tribler emits sparse payloads.
type Download struct {
	Name         string         `json:"name"`
	Progress     *float64       `json:"progress"`
	Infohash     string         `json:"infohash"`
	Status       DownloadStatus `json:"status"`
	AllTimeRatio *float64       `json:"all_time_ratio"`
	TimeAdded    *int64         `json:"time_added"`
	Eta          *float64       `json:"eta"`
	Error        string         `json:"error"`
	Size         *int64         `json:"size"`
	Destination  string         `json:"destination"`
}

// File ports C# `:142-149` — only the Name field is read at GetItems
// time (for the single-file OutputPath suffix).
type File struct {
	Name string `json:"name"`
}

// downloadsResponse + filesResponse are the top-level wrappers C#
// `:88-91, 151-154` use.
type downloadsResponse struct {
	Downloads []*Download `json:"downloads"`
}

type filesResponse struct {
	Files []*File `json:"files"`
}

// GetDownloads fires GET `/api/downloads`. Mirrors C#
// `TriblerDownloadClientProxy.GetDownloads` at `:85-90`.
func (p *Proxy) GetDownloads(ctx context.Context, s Settings) ([]*Download, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(s)+"/api/downloads", nil)
	if err != nil {
		return nil, fmt.Errorf("tribler: build downloads request: %w", err)
	}
	body, err := p.execute(req, s, "GetDownloads")
	if err != nil {
		return nil, err
	}
	var resp downloadsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("tribler: unmarshal downloads: %w", err)
	}
	return resp.Downloads, nil
}

// GetDownloadFiles fires GET `/api/downloads/<infohash>/files`.
// Mirrors C# `:79-83`. Used by GetItems to compose the single-file
// OutputPath suffix.
func (p *Proxy) GetDownloadFiles(ctx context.Context, s Settings, infohash string) ([]*File, error) {
	if strings.TrimSpace(infohash) == "" {
		return nil, fmt.Errorf("tribler: GetDownloadFiles: empty infohash")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(s)+"/api/downloads/"+infohash+"/files", nil)
	if err != nil {
		return nil, fmt.Errorf("tribler: build files request: %w", err)
	}
	body, err := p.execute(req, s, "GetDownloadFiles")
	if err != nil {
		return nil, err
	}
	var resp filesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("tribler: unmarshal files: %w", err)
	}
	return resp.Files, nil
}

// RemoveDownloadRequest is the wire body for `DELETE /api/downloads/<hash>`.
// Mirrors C# `Models/TriblerDownloadClientApi.cs:113-117` with the
// same JSON tag (snake_case via [JsonProperty]).
type RemoveDownloadRequest struct {
	RemoveData bool `json:"remove_data"`
}

// deleteDownloadResponse mirrors C# `DeleteDownloadResponse` at
// `:119-123`. The Provider doesn't surface either field but the
// envelope is decoded to confirm a 2xx body decodes cleanly.
type deleteDownloadResponse struct {
	Removed  *bool  `json:"removed,omitempty"`
	Infohash string `json:"infohash,omitempty"`
}

// RemoveDownload fires DELETE `/api/downloads/<infohash>` with the
// `{remove_data:bool}` body. Mirrors C#
// `TriblerDownloadClientProxy.RemoveDownload` at `:92-106`.
//
// Lowercases the infohash before composing the URL (matches C#
// `:99`'s `item.DownloadId.ToLower()`).
func (p *Proxy) RemoveDownload(ctx context.Context, s Settings, infohash string, deleteData bool) error {
	hash := strings.ToLower(strings.TrimSpace(infohash))
	if hash == "" {
		return fmt.Errorf("tribler: RemoveDownload: empty infohash")
	}
	body, err := json.Marshal(RemoveDownloadRequest{RemoveData: deleteData})
	if err != nil {
		return fmt.Errorf("tribler: marshal remove body: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, baseURL(s)+"/api/downloads/"+hash, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("tribler: build remove request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	respBody, err := p.execute(httpReq, s, "RemoveDownload")
	if err != nil {
		return err
	}
	// Decode the envelope; we don't inspect the fields but the
	// unmarshal catches malformed payloads.
	var resp deleteDownloadResponse
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return fmt.Errorf("tribler: unmarshal remove response: %w", err)
		}
	}
	return nil
}

// AddDownloadRequest is the wire body for `PUT /api/downloads`.
// Mirrors C# `Models/TriblerDownloadClientApi.cs:93-105` with the
// same JSON tags (snake_case via [JsonProperty]).
type AddDownloadRequest struct {
	AnonymityHops *int64 `json:"anon_hops,omitempty"`
	SafeSeeding   *bool  `json:"safe_seeding,omitempty"`
	Destination   string `json:"destination,omitempty"`
	URI           string `json:"uri"`
}

// addDownloadResponse mirrors C# `AddDownloadResponse` at `:107-111`.
type addDownloadResponse struct {
	Infohash string `json:"infohash"`
}

// AddFromMagnetLink fires PUT `/api/downloads` with the supplied
// request body. Returns the resulting torrent's infohash. Mirrors
// C# `TriblerDownloadClientProxy.AddFromMagnetLink` at `:108-117`.
//
// The C# request requires `uri`; the Go port enforces non-empty
// URI before posting.
func (p *Proxy) AddFromMagnetLink(ctx context.Context, s Settings, req AddDownloadRequest) (string, error) {
	if strings.TrimSpace(req.URI) == "" {
		return "", fmt.Errorf("tribler: AddFromMagnetLink: empty URI")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("tribler: marshal add body: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, baseURL(s)+"/api/downloads", strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("tribler: build add request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	respBody, err := p.execute(httpReq, s, "AddFromMagnetLink")
	if err != nil {
		return "", err
	}
	var resp addDownloadResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("tribler: unmarshal add response: %w", err)
	}
	if strings.TrimSpace(resp.Infohash) == "" {
		return "", fmt.Errorf("tribler: AddFromMagnetLink returned empty infohash")
	}
	return resp.Infohash, nil
}

// GetConfig fires GET `/api/settings`. Auth-required. Mirrors C#
// `TriblerDownloadClientProxy.GetConfig` at `:73-77`.
//
// Used by the Test arm — a successful GetConfig proves both
// connectivity AND the X-Api-Key header was accepted.
func (p *Proxy) GetConfig(ctx context.Context, s Settings) (*Config, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(s)+"/api/settings", nil)
	if err != nil {
		return nil, fmt.Errorf("tribler: build settings request: %w", err)
	}
	body, err := p.execute(req, s, "GetConfig")
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("tribler: unmarshal settings: %w", err)
	}
	return &cfg, nil
}

// execute fires one request with the X-Api-Key header applied and
// classifies the response. 401/403 → ErrAuth; non-2xx → ErrUnavailable.
// Mirrors C# `ProcessRequest` at `:50-71` with status-code-specific
// exceptions.
func (p *Proxy) execute(req *http.Request, s Settings, operation string) ([]byte, error) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Api-Key", s.ApiKey)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnavailable, operation, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("tribler: %s read body: %w", operation, err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: %s status %d", ErrAuth, operation, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: %s status %d", ErrUnavailable, operation, resp.StatusCode)
	}
	return body, nil
}

// ErrUnavailable wraps every transport-level failure (network
// errors + non-2xx other than 401/403). Mirrors C#
// `DownloadClientUnavailableException` at `:69`.
var ErrUnavailable = errors.New("tribler: unavailable")

// ErrAuth wraps 401/403 responses (bad ApiKey). Mirrors C#
// `DownloadClientAuthenticationException` at `:66`.
var ErrAuth = errors.New("tribler: authentication failed")
