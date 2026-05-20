package utorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// HTTPDoer is the slice of [*http.Client] the [Proxy] depends on.
// The stdlib `*http.Client` satisfies it; tests inject a stub.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Proxy is the cookie+token-authenticated HTTP client for a
// uTorrent endpoint. Mirrors C# `UTorrentProxy` at
// `UTorrentProxy.cs:30-264`.
//
// Auth flow (C# `:228-264`):
//
//  1. GET /<urlBase>/gui/token.html with Basic-Auth.
//  2. Parse `<div id='token' style='display:none;'>TOKEN</div>` from the
//     HTML body — that's the per-session token.
//  3. The response sets a GUID cookie that subsequent requests must
//     replay.
//  4. Every subsequent GET adds `?token=TOKEN&...` + the GUID cookie.
//  5. On 401/400 the token/cookie are stale → re-fetch via step 1.
//
// Goroutine-safety: the in-memory session (token + cookies) is
// guarded by a mutex; concurrent calls share the same session and
// trigger at most one re-auth at a time.
type Proxy struct {
	http HTTPDoer
	log  *slog.Logger

	mu      sync.Mutex
	token   string
	cookies []*http.Cookie
	authKey string // tracks "did settings change" so we drop stale session
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

// baseURL builds the absolute URL prefix for /gui/ requests.
// Mirrors C# `BuildRequest` constructor + `.Resource("/gui/")` at
// `:185-197`.
func baseURL(settings Settings) string {
	scheme := "http"
	if settings.UseSsl {
		scheme = "https"
	}
	urlBase := strings.Trim(strings.TrimSpace(settings.UrlBase), "/")
	if urlBase == "" {
		return fmt.Sprintf("%s://%s:%d/gui/", scheme, settings.Host, settings.Port)
	}
	return fmt.Sprintf("%s://%s:%d/%s/gui/", scheme, settings.Host, settings.Port, urlBase)
}

// authKeyFor produces the cache key the auth state belongs to.
// Matches the C# `"{baseUrl}:{password}"` key shape at `:230` —
// settings change (host/port/password change) drops the session.
func authKeyFor(settings Settings) string {
	return baseURL(settings) + ":" + settings.Password
}

// ErrAuthFailed is returned when the token-fetch response is 401.
// Typically means Settings.Username/Password are wrong or the
// uTorrent WebUI requires login (the C# tree raises
// DownloadClientAuthenticationException for the same case).
var ErrAuthFailed = errors.New("utorrent: authentication failed (check Username + Password)")

// tokenRegex extracts the per-session token from the
// /gui/token.html body. The C# tree parses the body as XML and
// reads `FirstChild.FirstChild.InnerText` — works because the
// token.html body is a small, self-contained `<html><div>TOKEN</div></html>`
// fragment. The Go port uses a regex because the body isn't
// guaranteed-valid XML across uTorrent builds (some emit
// inline-style with single quotes that html.Parse needs but
// encoding/xml rejects).
var tokenRegex = regexp.MustCompile(`<div[^>]*id\s*=\s*['"]token['"][^>]*>([^<]+)</div>`)

// authenticate fetches /gui/token.html, extracts the per-session
// token, and captures the GUID cookies for replay. force=true
// drops any existing session (used on re-auth after a stale-token
// response).
func (p *Proxy) authenticate(ctx context.Context, settings Settings, force bool) error {
	key := authKeyFor(settings)

	p.mu.Lock()
	hasSession := !force && p.authKey == key && p.token != "" && len(p.cookies) > 0
	p.mu.Unlock()
	if hasSession {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(settings)+"token.html", nil)
	if err != nil {
		return fmt.Errorf("utorrent: build token request: %w", err)
	}
	if settings.Username != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("utorrent: token fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrAuthFailed
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("utorrent: token read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("utorrent: token fetch status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	m := tokenRegex.FindStringSubmatch(string(body))
	if len(m) < 2 || strings.TrimSpace(m[1]) == "" {
		return errors.New("utorrent: token not found in /gui/token.html response")
	}

	p.mu.Lock()
	p.authKey = key
	p.token = strings.TrimSpace(m[1])
	p.cookies = resp.Cookies()
	p.mu.Unlock()
	return nil
}

// snapshot returns the current session (token + cookies) for the
// settings key, or empty when the session is missing.
func (p *Proxy) snapshot(settings Settings) (token string, cookies []*http.Cookie, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.authKey != authKeyFor(settings) || p.token == "" {
		return "", nil, false
	}
	out := make([]*http.Cookie, len(p.cookies))
	copy(out, p.cookies)
	return p.token, out, true
}

// call submits a token-authenticated GET to /gui/?<query>. The
// query already contains the per-action params; this method
// prepends the token. On 400/401 the session is invalidated and a
// single re-auth + retry happens (matches C# `:208-219`).
//
// Returns the response body bytes — callers parse as
// [Response].
func (p *Proxy) call(ctx context.Context, settings Settings, query url.Values) ([]byte, error) {
	return p.callInner(ctx, settings, query, true)
}

func (p *Proxy) callInner(ctx context.Context, settings Settings, query url.Values, allowReauth bool) ([]byte, error) {
	if err := p.authenticate(ctx, settings, false); err != nil {
		return nil, err
	}
	token, cookies, ok := p.snapshot(settings)
	if !ok {
		return nil, errors.New("utorrent: no session after authenticate")
	}

	q := url.Values{}
	q.Set("token", token)
	for k, vv := range query {
		for _, v := range vv {
			q.Add(k, v)
		}
	}

	endpoint := baseURL(settings) + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("utorrent: build request: %w", err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if settings.Username != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("utorrent: %s: %w", query.Get("action"), err)
	}
	defer func() { _ = resp.Body.Close() }()

	if (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized) && allowReauth {
		// Stale session — drop + retry once.
		p.mu.Lock()
		p.authKey = ""
		p.token = ""
		p.cookies = nil
		p.mu.Unlock()
		if err := p.authenticate(ctx, settings, true); err != nil {
			return nil, err
		}
		return p.callInner(ctx, settings, query, false)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrAuthFailed
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("utorrent: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("utorrent: status %d: %s", resp.StatusCode, strings.TrimSpace(truncate(string(body), 256)))
	}
	return body, nil
}

// Response is the JSON envelope uTorrent's /gui/ returns. Mirrors
// C# `UTorrentResponse` at `UTorrentResponse.cs:7-25`. Only the
// fields the Go-port slices read are surfaced; the rest decode to
// the wire-shape but stay unused (Settings table for GetConfig,
// CacheNumber for the GetItems delta cache, etc.).
type Response struct {
	Build           int        `json:"build"`
	Torrents        [][]any    `json:"torrents"`
	Label           [][]string `json:"label"`
	TorrentsChanged [][]any    `json:"torrentp"`
	TorrentsRemoved []string   `json:"torrentm"`
	CacheNumber     string     `json:"torrentc"`
	Settings        [][]any    `json:"settings"`
}

// Status is the uTorrent per-torrent status bitflag. Ports C#
// `UTorrentTorrentStatus` at `UTorrentTorrentStatus.cs:5-15`. The
// flag values are stable across the wire — uTorrent's list=1
// response stores the status as a single int with these bits OR'd
// together.
type Status int

// Status bitflag values. Mirrors C# `UTorrentTorrentStatus`
// enum members.
const (
	StatusStarted         Status = 1
	StatusChecking        Status = 2
	StatusStartAfterCheck Status = 4
	StatusChecked         Status = 8
	StatusError           Status = 16
	StatusPaused          Status = 32
	StatusQueued          Status = 64
	StatusLoaded          Status = 128
)

// Has reports whether the status int has the given flag set.
func (s Status) Has(flag Status) bool { return s&flag != 0 }

// Torrent is the post-decoded uTorrent row shape. Mirrors C#
// `UTorrentTorrent` at `UTorrentTorrent.cs:7-32` (subset — the
// fields the Go-port GetItems consumes; the speculative
// AppUpdateUrl / Unknown27/28 trailing slots are skipped).
type Torrent struct {
	Hash             string
	Status           Status
	Name             string
	Size             int64
	Progress         float64 // 0.0 .. 1.0 (uTorrent emits per-mille int; converted at decode)
	Downloaded       int64
	Uploaded         int64
	Ratio            float64 // ratio in float (uTorrent emits per-mille int; converted at decode)
	UploadSpeed      int64
	DownloadSpeed    int64
	Eta              int // seconds; -1 → no ETA
	Label            string
	PeersConnected   int
	PeersInSwarm     int
	SeedsConnected   int
	SeedsInSwarm     int
	Availability     float64 // uTorrent emits int / 65536.0
	QueueOrder       int
	Remaining        int64
	DownloadURL      string
	RootDownloadPath string
}

// GetTorrents fires action=list with list=1 and decodes the
// per-row Torrents array. Mirrors C# `GetTorrents` at
// `UTorrentProxy.cs:69-87` (without the cacheID delta — the Go-port
// always full-refreshes for now; the cache-delta optimization is a
// follow-up).
func (p *Proxy) GetTorrents(ctx context.Context, settings Settings) ([]Torrent, error) {
	body, err := p.call(ctx, settings, url.Values{"list": []string{"1"}})
	if err != nil {
		return nil, err
	}
	var parsed Response
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("utorrent: decode list: %w", err)
	}
	out := make([]Torrent, 0, len(parsed.Torrents))
	for _, row := range parsed.Torrents {
		if t, ok := decodeTorrentRow(row); ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// decodeTorrentRow projects one untyped array row into [Torrent].
// Mirrors C# `UTorrentTorrentJsonConverter.ReadJson` at `:43-110`.
// Returns ok=false when the row has fewer than 19 columns
// (uTorrent's minimum stable shape from build 25406 — see
// UTorrentTorrentJsonConverter comment at `:80`).
//
// Build-variation tolerance: the trailing columns (DownloadURL,
// RssFeedUrl, ..., RootDownloadPath) are populated only when the
// row has them; missing-trailing columns leave the Torrent fields
// at their zero values (matches C# `:80-102` "Builds before 25406
// don't return the remaining items" guard).
func decodeTorrentRow(row []any) (Torrent, bool) {
	if len(row) < 19 {
		return Torrent{}, false
	}
	t := Torrent{
		Hash:           asString(row[0]),
		Status:         Status(asInt(row[1])),
		Name:           asString(row[2]),
		Size:           asInt64(row[3]),
		Progress:       float64(asInt(row[4])) / 1000.0,
		Downloaded:     asInt64(row[5]),
		Uploaded:       asInt64(row[6]),
		Ratio:          float64(asInt(row[7])) / 1000.0,
		UploadSpeed:    asInt64(row[8]),
		DownloadSpeed:  asInt64(row[9]),
		Eta:            asInt(row[10]),
		Label:          asString(row[11]),
		PeersConnected: asInt(row[12]),
		PeersInSwarm:   asInt(row[13]),
		SeedsConnected: asInt(row[14]),
		SeedsInSwarm:   asInt(row[15]),
		Availability:   float64(asInt(row[16])) / 65536.0,
		QueueOrder:     asInt(row[17]),
		Remaining:      asInt64(row[18]),
	}
	if len(row) > 19 {
		t.DownloadURL = asString(row[19])
	}
	if len(row) > 26 {
		t.RootDownloadPath = asString(row[26])
	}
	return t, true
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

// GetVersion fires action=getsettings and returns the `build`
// integer from the response. Mirrors C# `GetVersion` at `:48-55`.
func (p *Proxy) GetVersion(ctx context.Context, settings Settings) (int, error) {
	body, err := p.call(ctx, settings, url.Values{"action": []string{"getsettings"}})
	if err != nil {
		return 0, err
	}
	var parsed Response
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("utorrent: decode getsettings: %w", err)
	}
	return parsed.Build, nil
}

// AddTorrentFromURL fires action=add-url with the torrent URL /
// magnet link. Mirrors C# `AddTorrentFromUrl` at `:90-97`.
func (p *Proxy) AddTorrentFromURL(ctx context.Context, torrentURL string, settings Settings) error {
	_, err := p.call(ctx, settings, url.Values{
		"action": []string{"add-url"},
		"s":      []string{torrentURL},
	})
	return err
}

// AddTorrentFromFile fires POST action=add-file with the .torrent
// bytes as a multipart/form-data upload. Mirrors C#
// `AddTorrentFromFile` at `:99-108`.
func (p *Proxy) AddTorrentFromFile(ctx context.Context, fileName string, content []byte, settings Settings) error {
	q := url.Values{
		"action": []string{"add-file"},
		"path":   []string{""},
	}
	_, err := p.callMultipart(ctx, settings, q, fileName, content)
	return err
}

// SetTorrentLabel fires action=setprops&hash=<hash>&s=label&v=<label>.
// Mirrors C# `SetTorrentLabel` at `:141-150`.
func (p *Proxy) SetTorrentLabel(ctx context.Context, hash, label string, settings Settings) error {
	_, err := p.call(ctx, settings, url.Values{
		"action": []string{"setprops"},
		"hash":   []string{strings.ToLower(hash)},
		"s":      []string{"label"},
		"v":      []string{label},
	})
	return err
}

// MoveTorrentToTopInQueue fires action=queuetop&hash=. Mirrors C#
// `MoveTorrentToTopInQueue` at `:166-172`.
func (p *Proxy) MoveTorrentToTopInQueue(ctx context.Context, hash string, settings Settings) error {
	_, err := p.call(ctx, settings, url.Values{
		"action": []string{"queuetop"},
		"hash":   []string{strings.ToLower(hash)},
	})
	return err
}

// SetState fires action=<state>&hash= where <state> is the
// lower-cased State name. Mirrors C# `SetState` at `:174-180`.
func (p *Proxy) SetState(ctx context.Context, hash string, state State, settings Settings) error {
	_, err := p.call(ctx, settings, url.Values{
		"action": []string{strings.ToLower(state.String())},
		"hash":   []string{strings.ToLower(hash)},
	})
	return err
}

// RemoveTorrent fires action=remove (or action=removedata when
// removeData) with the hash. Mirrors C# `RemoveTorrent` at
// `:130-139`.
func (p *Proxy) RemoveTorrent(ctx context.Context, hash string, removeData bool, settings Settings) error {
	action := "remove"
	if removeData {
		action = "removedata"
	}
	_, err := p.call(ctx, settings, url.Values{
		"action": []string{action},
		"hash":   []string{strings.ToLower(hash)},
	})
	return err
}

// callMultipart submits a token-authenticated POST with a
// multipart/form-data body containing a single torrent_file part.
// Used by AddTorrentFromFile. The token replay + cookie attach
// match the GET path; on stale-session 400/401 a single re-auth +
// retry happens like the GET arm.
func (p *Proxy) callMultipart(ctx context.Context, settings Settings, query url.Values, fileName string, content []byte) ([]byte, error) {
	return p.callMultipartInner(ctx, settings, query, fileName, content, true)
}

func (p *Proxy) callMultipartInner(ctx context.Context, settings Settings, query url.Values, fileName string, content []byte, allowReauth bool) ([]byte, error) {
	if err := p.authenticate(ctx, settings, false); err != nil {
		return nil, err
	}
	token, cookies, ok := p.snapshot(settings)
	if !ok {
		return nil, errors.New("utorrent: no session after authenticate")
	}

	q := url.Values{}
	q.Set("token", token)
	for k, vv := range query {
		for _, v := range vv {
			q.Add(k, v)
		}
	}
	endpoint := baseURL(settings) + "?" + q.Encode()

	body, contentType, err := buildTorrentFileMultipart(fileName, content)
	if err != nil {
		return nil, fmt.Errorf("utorrent: build multipart: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("utorrent: build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if settings.Username != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("utorrent: %s: %w", query.Get("action"), err)
	}
	defer func() { _ = resp.Body.Close() }()

	if (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized) && allowReauth {
		p.mu.Lock()
		p.authKey = ""
		p.token = ""
		p.cookies = nil
		p.mu.Unlock()
		if err := p.authenticate(ctx, settings, true); err != nil {
			return nil, err
		}
		return p.callMultipartInner(ctx, settings, query, fileName, content, false)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrAuthFailed
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("utorrent: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("utorrent: status %d: %s", resp.StatusCode, strings.TrimSpace(truncate(string(respBody), 256)))
	}
	return respBody, nil
}

// buildTorrentFileMultipart constructs a multipart/form-data body
// with a single torrent_file part containing the .torrent bytes.
// Returns the reader, the Content-Type header value (including the
// boundary), or an error.
func buildTorrentFileMultipart(fileName string, content []byte) (*bytes.Buffer, string, error) {
	if strings.TrimSpace(fileName) == "" {
		fileName = "upload.torrent"
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("torrent_file", fileName)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(content); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
