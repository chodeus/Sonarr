package rtorrent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// urlQueryUnescape is a renamed alias for net/url.QueryUnescape
// (referenced via the small wrapper above so the GetTorrents body
// reads cleanly). Inlined into a const-style binding via the
// re-export wrapper below.
var urlQueryUnescape = url.QueryUnescape

// HTTPDoer is the slice of [*http.Client] the [Proxy] depends on.
// The stdlib `*http.Client` satisfies it; tests inject a stub.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Proxy is the XML-RPC client for an rTorrent endpoint. Mirrors C#
// `RTorrentProxy` at `RTorrentProxy.cs:28-217`. The endpoint URL is
// built per-call from the settings (so a single Proxy can serve
// multiple Settings instances — matches the C#
// `RTorrentSettings settings` per-method-arg pattern).
type Proxy struct {
	http HTTPDoer
	log  *slog.Logger
}

// NewProxy constructs a Proxy. http defaults to http.DefaultClient
// when nil; log defaults to slog.Default.
func NewProxy(httpDoer HTTPDoer, log *slog.Logger) *Proxy {
	if httpDoer == nil {
		httpDoer = http.DefaultClient
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{http: httpDoer, log: log}
}

// endpointURL builds the absolute URL the XML-RPC POST targets.
// Mirrors C# `XmlRpcRequestBuilder` constructor at
// `RTorrentProxy.cs:185-191` — scheme from UseSsl, host/port from
// settings, path = UrlBase (default "RPC2"). Empty UrlBase falls
// back to "RPC2" matching the C# default at
// `RTorrentSettings.cs:27`.
func endpointURL(settings Settings) string {
	scheme := "http"
	if settings.UseSsl {
		scheme = "https"
	}
	urlBase := strings.TrimSpace(settings.UrlBase)
	urlBase = strings.Trim(urlBase, "/")
	if urlBase == "" {
		urlBase = "RPC2"
	}
	return fmt.Sprintf("%s://%s:%d/%s", scheme, settings.Host, settings.Port, urlBase)
}

// call submits an XML-RPC methodCall and returns the parsed
// response's first param value. FaultError is surfaced verbatim;
// HTTP-level failures wrap the underlying error.
func (p *Proxy) call(ctx context.Context, settings Settings, method string, args ...any) (*rawValue, error) {
	body, err := encodeCall(method, args...)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL(settings), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rtorrent: build request: %w", err)
	}
	req.Header.Set("Content-Type", "text/xml")
	req.Header.Set("Accept", "text/xml")
	if strings.TrimSpace(settings.Username) != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rtorrent: %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("rtorrent: %s read body: %w", method, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rtorrent: %s status %d: %s", method, resp.StatusCode, strings.TrimSpace(truncate(string(respBody), 256)))
	}

	value, err := decodeResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("rtorrent: %s: %w", method, err)
	}
	return value, nil
}

// ErrUnauthorized is returned when the rTorrent endpoint responds
// 401 — typically because Settings.Username/Password are wrong or
// the rTorrent .htaccess rejected the credentials.
var ErrUnauthorized = errors.New("rtorrent: 401 Unauthorized (check Username + Password)")

// GetVersion fires system.client_version. Mirrors C# `:35-40`.
// Returns "0.0.0" when the response is missing a <string> value
// (matches the C# `.FirstOrDefault()?.Value ?? "0.0.0"` posture).
func (p *Proxy) GetVersion(ctx context.Context, settings Settings) (string, error) {
	value, err := p.call(ctx, settings, "system.client_version")
	if err != nil {
		return "", err
	}
	if v := value.stringValue(); v != "" {
		return v, nil
	}
	return "0.0.0", nil
}

// AddTorrentFromURL fires load.start (or load.normal when
// settings.AddStopped) with the torrent URL / magnet link. Mirrors
// C# `RTorrentProxy.AddTorrentFromUrl` at `:70-89`.
//
// Args wire-shape:
//
//	["", torrentURL, "d.custom1.set=<label>", "d.priority.set=<p>", "d.directory.set=<dir>"]
//
// where the trailing per-d-command strings are emitted only when
// label / priority / directory are non-empty (priority != Normal
// inhibits emission per C# `:170-175`).
//
// Returns the non-zero fault-equivalent integer response as an
// error to mirror C# `:78-82` `response.GetIntResponse() != 0` arm.
func (p *Proxy) AddTorrentFromURL(ctx context.Context, torrentURL, label string, priority Priority, directory string, settings Settings) error {
	method := "load.start"
	if settings.AddStopped {
		method = "load.normal"
	}
	args := append([]any{"", torrentURL}, dCommands(label, priority, directory)...)
	value, err := p.call(ctx, settings, method, args...)
	if err != nil {
		return err
	}
	if v := value.intValue(); v != 0 {
		return fmt.Errorf("rtorrent: could not add torrent (load returned %d): %s", v, torrentURL)
	}
	return nil
}

// AddTorrentFromFile fires load.raw_start (or load.raw when
// settings.AddStopped) with the .torrent file bytes as the base64-
// encoded second arg. Mirrors C# `RTorrentProxy.AddTorrentFromFile`
// at `:91-110`.
func (p *Proxy) AddTorrentFromFile(ctx context.Context, fileName string, content []byte, label string, priority Priority, directory string, settings Settings) error {
	method := "load.raw_start"
	if settings.AddStopped {
		method = "load.raw"
	}
	args := append([]any{"", content}, dCommands(label, priority, directory)...)
	value, err := p.call(ctx, settings, method, args...)
	if err != nil {
		return err
	}
	if v := value.intValue(); v != 0 {
		return fmt.Errorf("rtorrent: could not add torrent (load_raw returned %d): %s", v, fileName)
	}
	return nil
}

// Torrent is the post-decoded shape returned by [Proxy.GetTorrents].
// Mirrors C# `RTorrentTorrent` at `RTorrentTorrent.cs:9-46`.
type Torrent struct {
	Name          string
	Hash          string
	Path          string
	Category      string
	TotalSize     int64
	RemainingSize int64
	DownRate      int64
	Ratio         int64 // rTorrent ratio in per-mille (1000 = 1.0)
	IsOpen        bool
	IsActive      bool
	IsFinished    bool
	FinishedTime  int64 // unix seconds
}

// GetTorrents fires d.multicall.2 against the empty target view
// with the 12-field column projection. Mirrors C#
// `RTorrentProxy.GetTorrents` at `:42-67`. The C# code uses
// `HttpUtility.UrlDecode` on the custom1 column (rTorrent stores
// labels URL-encoded); the Go port uses net/url.QueryUnescape which
// is the stdlib equivalent (returns the input unchanged on a
// decode failure — matches the C# permissive behaviour).
func (p *Proxy) GetTorrents(ctx context.Context, settings Settings) ([]Torrent, error) {
	value, err := p.call(ctx, settings,
		"d.multicall.2",
		"",
		"",
		"d.name=",
		"d.hash=",
		"d.base_path=",
		"d.custom1=",
		"d.size_bytes=",
		"d.left_bytes=",
		"d.down.rate=",
		"d.ratio=",
		"d.is_open=",
		"d.is_active=",
		"d.complete=",
		"d.timestamp.finished=",
	)
	if err != nil {
		return nil, err
	}
	rows := value.elements()
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]Torrent, 0, len(rows))
	for _, row := range rows {
		cols := row.elements()
		if len(cols) < 12 {
			// Partial row — skip silently rather than indexing OOB.
			continue
		}
		category := cols[3].stringValue()
		if dec, err := urlUnescape(category); err == nil {
			category = dec
		}
		out = append(out, Torrent{
			Name:          cols[0].stringValue(),
			Hash:          cols[1].stringValue(),
			Path:          cols[2].stringValue(),
			Category:      category,
			TotalSize:     cols[4].int64Value(),
			RemainingSize: cols[5].int64Value(),
			DownRate:      cols[6].int64Value(),
			Ratio:         cols[7].int64Value(),
			IsOpen:        cols[8].int64Value() != 0,
			IsActive:      cols[9].int64Value() != 0,
			IsFinished:    cols[10].int64Value() != 0,
			FinishedTime:  cols[11].int64Value(),
		})
	}
	return out, nil
}

// urlUnescape is a small wrapper to keep the GetTorrents body free
// of import noise. net/url.QueryUnescape is the stdlib equivalent
// of C# HttpUtility.UrlDecode for the +→ space + %xx-decode subset.
func urlUnescape(s string) (string, error) {
	return urlQueryUnescape(s)
}

// dCommands builds the per-d-method "command string" args appended
// to load.start / load.raw_start. Mirrors C# `GetCommands(label,
// priority, directory)` at `:160-178`. The d.priority.set arm is
// elided when priority == Normal (matches C# `:170-174`).
func dCommands(label string, priority Priority, directory string) []any {
	var out []any
	if strings.TrimSpace(label) != "" {
		out = append(out, "d.custom1.set="+label)
	}
	if priority != PriorityNormal {
		out = append(out, fmt.Sprintf("d.priority.set=%d", int(priority)))
	}
	if strings.TrimSpace(directory) != "" {
		out = append(out, "d.directory.set="+directory)
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
