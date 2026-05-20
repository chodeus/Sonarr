package rqbit

import (
	"bytes"
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

// Proxy is the HTTP client for an RQBit endpoint. Mirrors C#
// `RQbitProxy` at `RQbitProxy.cs:24-233`. RQBit has no
// authentication so the proxy is stateless.
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

// baseURL composes the absolute base. Mirrors C# `BuildRequest` at
// `:224-232` — `<scheme>://<host>:<port><urlBase>`. UrlBase is
// trimmed of trailing `/` so concatenation produces clean paths.
func baseURL(s Settings) string {
	scheme := "http"
	if s.UseSsl {
		scheme = "https"
	}
	ub := strings.TrimSpace(s.UrlBase)
	if ub == "" {
		ub = "/"
	}
	if !strings.HasPrefix(ub, "/") {
		ub = "/" + ub
	}
	return fmt.Sprintf("%s://%s:%d%s", scheme, s.Host, s.Port, strings.TrimRight(ub, "/"))
}

// rootResponse mirrors C# `ResponseModels/RootResponse.cs`.
type rootResponse struct {
	Server  string `json:"server"`
	Version string `json:"version"`
}

// torrentState ports C# `ResponseModels/TorrentState.cs:3-9`. The
// wire shape is a string ("initializing"/"paused"/...); the
// UnmarshalJSON hook maps to the typed value.
type torrentState int

const (
	stateInitializing torrentState = 0
	statePaused       torrentState = 1
	stateLive         torrentState = 2
	stateError        torrentState = 3
	stateInvalid      torrentState = 4
)

// UnmarshalJSON maps the snake-case state string to the typed
// torrentState enum. Numeric input is also accepted for
// compatibility with RQBit builds that might emit ints.
func (s *torrentState) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		// Some RQBit builds may emit the numeric form — accept too.
		var n int
		if err2 := json.Unmarshal(data, &n); err2 == nil {
			*s = torrentState(n)
			return nil
		}
		return err
	}
	switch strings.ToLower(raw) {
	case "initializing":
		*s = stateInitializing
	case "paused":
		*s = statePaused
	case "live":
		*s = stateLive
	case "error":
		*s = stateError
	case "invalid":
		*s = stateInvalid
	default:
		*s = stateError
	}
	return nil
}

// torrentSpeed mirrors C# `TorrentSpeedResponse` at
// `ListTorrentsWithStatsResponse.cs:78-83`. Only `mbps` is read.
type torrentSpeed struct {
	Mbps float64 `json:"mbps"`
}

// torrentLiveStats wraps the per-call download-speed snapshot.
type torrentLiveStats struct {
	DownloadSpeed *torrentSpeed `json:"download_speed"`
}

// torrentStats ports the relevant subset of C# `TorrentStatsResponse`.
type torrentStats struct {
	State         torrentState      `json:"state"`
	Error         string            `json:"error"`
	ProgressBytes int64             `json:"progress_bytes"`
	UploadedBytes int64             `json:"uploaded_bytes"`
	TotalBytes    int64             `json:"total_bytes"`
	Finished      bool              `json:"finished"`
	Live          *torrentLiveStats `json:"live"`
}

// torrentWithStats ports the per-row shape under the `torrents`
// key of /torrents?with_stats=true.
type torrentWithStats struct {
	ID           int64        `json:"id"`
	InfoHash     string       `json:"info_hash"`
	Name         string       `json:"name"`
	OutputFolder string       `json:"output_folder"`
	Stats        torrentStats `json:"stats"`
}

// listTorrentsResponse wraps the top-level list payload.
type listTorrentsResponse struct {
	Torrents []*torrentWithStats `json:"torrents"`
}

// addResponse mirrors C# `PostTorrentResponse`.
type addResponse struct {
	ID      int64               `json:"id"`
	Details *addResponseDetails `json:"details"`
}

type addResponseDetails struct {
	InfoHash string `json:"info_hash"`
	Name     string `json:"name"`
}

// Torrent is the proxy-side projection consumers see (mirrors C#
// `RQBitTorrent.cs:3-17` minus the unused FinishedTime + Category
// fields the Go port doesn't carry). Computed once by [Proxy.GetTorrents]
// from the per-row wire shape.
type Torrent struct {
	ID            int64
	Name          string
	Hash          string
	TotalSize     int64
	RemainingSize int64
	Ratio         float64
	DownRate      int64
	IsFinished    bool
	IsActive      bool
	HasError      bool
	Path          string
	Message       string
}

// GetVersion fires GET `/` and returns the `version` field. Used
// by Test. Mirrors C# `RQbitProxy.GetVersion` at `:60-76`.
func (p *Proxy) GetVersion(ctx context.Context, s Settings) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(s)+"/", nil)
	if err != nil {
		return "", fmt.Errorf("rqbit: build version request: %w", err)
	}
	body, err := p.execute(req, "GetVersion")
	if err != nil {
		return "", err
	}
	var root rootResponse
	if err := json.Unmarshal(body, &root); err != nil {
		return "", fmt.Errorf("rqbit: unmarshal version response: %w", err)
	}
	return root.Version, nil
}

// GetTorrents fires GET `/torrents?with_stats=true` and projects
// the per-row wire shape into the consumer-facing [Torrent] slice.
// Mirrors C# `RQbitProxy.GetTorrents` at `:78-140`.
func (p *Proxy) GetTorrents(ctx context.Context, s Settings) ([]*Torrent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(s)+"/torrents?with_stats=true", nil)
	if err != nil {
		return nil, fmt.Errorf("rqbit: build list request: %w", err)
	}
	body, err := p.execute(req, "GetTorrents")
	if err != nil {
		return nil, err
	}
	var raw listTorrentsResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("rqbit: unmarshal torrent list: %w", err)
	}
	out := make([]*Torrent, 0, len(raw.Torrents))
	for _, t := range raw.Torrents {
		if t == nil {
			continue
		}
		out = append(out, projectTorrent(t))
	}
	return out, nil
}

// projectTorrent maps one wire row into the consumer projection.
// Mirrors the per-torrent block at C# `:98-130`.
func projectTorrent(t *torrentWithStats) *Torrent {
	tp := &Torrent{
		ID:            t.ID,
		Name:          t.Name,
		Hash:          t.InfoHash,
		TotalSize:     t.Stats.TotalBytes,
		RemainingSize: t.Stats.TotalBytes - t.Stats.ProgressBytes,
		IsFinished:    t.Stats.Finished,
		IsActive:      t.Stats.State == stateLive || t.Stats.State == stateInitializing,
		HasError:      t.Stats.State == stateError || t.Stats.State == stateInvalid,
		Message:       t.Stats.Error,
		Path:          t.OutputFolder + t.Name,
	}
	if t.Stats.Live != nil && t.Stats.Live.DownloadSpeed != nil {
		// Convert mbps to bytes/sec (matches C# `:108-110`'s
		// `Mbps * 1048576`).
		tp.DownRate = int64(t.Stats.Live.DownloadSpeed.Mbps * 1048576)
	}
	if t.Stats.ProgressBytes > 0 {
		tp.Ratio = float64(t.Stats.UploadedBytes) / float64(t.Stats.ProgressBytes)
	}
	return tp
}

// AddTorrentFromURL fires POST `/torrents?overwrite=true` with the
// magnet/url as the request body. Returns the resolved InfoHash.
// Mirrors C# `AddTorrentFromUrl` at `:149-168`. Empty Details
// surfaces an error rather than the C# `return null`.
func (p *Proxy) AddTorrentFromURL(ctx context.Context, s Settings, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(s)+"/torrents?overwrite=true", strings.NewReader(url))
	if err != nil {
		return "", fmt.Errorf("rqbit: build add-url request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	body, err := p.execute(req, "AddTorrentFromURL")
	if err != nil {
		return "", err
	}
	return parseAddResponse(body)
}

// AddTorrentFromFile fires POST `/torrents?overwrite=true` with the
// raw .torrent bytes as the request body. Mirrors C#
// `AddTorrentFromFile` at `:170-192`.
func (p *Proxy) AddTorrentFromFile(ctx context.Context, s Settings, contents []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(s)+"/torrents?overwrite=true", bytes.NewReader(contents))
	if err != nil {
		return "", fmt.Errorf("rqbit: build add-file request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	body, err := p.execute(req, "AddTorrentFromFile")
	if err != nil {
		return "", err
	}
	return parseAddResponse(body)
}

// parseAddResponse unwraps the standard POST /torrents response.
// Mirrors C# `:160-167,184-191`.
func parseAddResponse(body []byte) (string, error) {
	var resp addResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("rqbit: unmarshal add response: %w", err)
	}
	if resp.Details == nil || strings.TrimSpace(resp.Details.InfoHash) == "" {
		return "", fmt.Errorf("%w: add returned empty Details.InfoHash", ErrRPC)
	}
	return resp.Details.InfoHash, nil
}

// RemoveTorrent fires POST `/torrents/<hash>/forget` (removeData=false)
// or `/delete` (removeData=true). Mirrors C# `RemoveTorrent` at
// `:142-147`.
func (p *Proxy) RemoveTorrent(ctx context.Context, s Settings, hash string, removeData bool) error {
	suffix := "/forget"
	if removeData {
		suffix = "/delete"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(s)+"/torrents/"+hash+suffix, nil)
	if err != nil {
		return fmt.Errorf("rqbit: build remove request: %w", err)
	}
	_, err = p.execute(req, "RemoveTorrent")
	return err
}

// execute fires one request and returns the raw body. Non-2xx
// surfaces ErrUnavailable; transport failure too.
func (p *Proxy) execute(req *http.Request, operation string) ([]byte, error) {
	req.Header.Set("Accept", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnavailable, operation, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("rqbit: %s read body: %w", operation, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: %s status %d", ErrUnavailable, operation, resp.StatusCode)
	}
	return body, nil
}

// ErrUnavailable wraps every transport-level failure (network
// errors + non-2xx). RQBit has no auth so there's no ErrAuth.
var ErrUnavailable = errors.New("rqbit: unavailable")

// ErrRPC wraps an empty / malformed response payload that
// shouldn't have happened on a 2xx (e.g. add returned null
// Details). Mirrors the C# null-return arms that bubble up as a
// "Failed to add" exception in the Provider.
var ErrRPC = errors.New("rqbit: rpc error")
