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

// Config ports the minimal subset of C# `TriblerSettingsResponse`
// the Test arm needs. Tribler's `/api/settings` payload nests the
// useful bits under `settings.libtorrent.download_defaults.saveas`;
// the full path resolver is deferred — for now Test only proves
// the endpoint+auth works by issuing the GET.
//
// The Go-port `Config` struct surfaces the same `Settings.LibTorrent.DownloadDefaults.SaveAS`
// path so the eventual GetStatus + GetDownloadDirectory ports can
// reach it.
type Config struct {
	Settings struct {
		LibTorrent struct {
			DownloadDefaults struct {
				SaveAs string `json:"saveas"`
			} `json:"download_defaults"`
		} `json:"libtorrent"`
	} `json:"settings"`
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
