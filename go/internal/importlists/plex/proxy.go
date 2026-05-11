package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// httpDoer is the minimal HTTP-client surface the proxy needs.
// Pattern-mirror of the internal/importlists/sonarr.httpDoer
// precedent. Kept tiny so tests can swap in an httptest.Server-
// backed client without dragging the full *httpclient.Client
// surface through.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrRequestFailed wraps any transport-level failure (DNS / TCP /
// non-2xx HTTP status). Mirrors C# `NzbDroneClientException`
// raised by `PlexTvProxy.ProcessRequest` at
// `PlexTvProxy.cs:108-130` for any HttpException or WebException.
var ErrRequestFailed = errors.New("plex-import: request failed")

// ErrBadRequest is returned by the OAuth RequestAction dispatcher
// when a required query param is missing. Mirrors C#
// `BadRequestException` at `PlexImport.cs:69/74/79/90/106`.
var ErrBadRequest = errors.New("plex-import: bad request")

// Default plex.tv host endpoints. Ported from the literal URL
// strings `PlexTvService.cs:39 + 65 + 96 + 115` and
// `PlexTvProxy.cs:96`. Each Proxy instance captures these at
// construction time so tests can point a fresh Proxy at an
// httptest.Server without touching package state.
const (
	defaultHostPlexTv         = "https://plex.tv"
	defaultHostPlexTvAuth     = "https://app.plex.tv"
	defaultHostPlexTvDiscover = "https://discover.provider.plex.tv"
)

// Proxy ports `PlexTvService` at
// `src/NzbDrone.Core/Notifications/Plex/PlexTv/PlexTvService.cs:22-139`
// — the subset the import-list family needs.
//
// Hosts are captured per-Proxy at construction time so concurrent
// tests can each point a Proxy at its own httptest.Server without
// global-state races (parallel tests broke when these were
// package-level vars).
//
// Concurrency: stateless after construction — every method builds
// a fresh request and reads the response synchronously. Safe for
// concurrent use from the orchestrator's worker pool.
type Proxy struct {
	doer             httpDoer
	clientIdentifier string
	appName          string
	appVersion       string

	hostPlexTv         string
	hostPlexTvAuth     string
	hostPlexTvDiscover string
}

// ProxyOption is the functional-option type for [NewProxy]
// overrides. Tests use [WithHosts] to redirect outbound calls; the
// default Proxy hits real plex.tv.
type ProxyOption func(*Proxy)

// WithHosts overrides the three plex.tv host endpoints. Tests use
// this to point a fresh Proxy at an httptest.Server. Pass empty
// strings to keep the default for that slot.
func WithHosts(plexTv, plexTvAuth, plexTvDiscover string) ProxyOption {
	return func(p *Proxy) {
		if plexTv != "" {
			p.hostPlexTv = plexTv
		}
		if plexTvAuth != "" {
			p.hostPlexTvAuth = plexTvAuth
		}
		if plexTvDiscover != "" {
			p.hostPlexTvDiscover = plexTvDiscover
		}
	}
}

// NewProxy binds an httpDoer + per-install identification triple
// into a Proxy. Empty identifier falls back to "sonarr" (the C#
// code reads `IConfigService.PlexClientIdentifier` which is a
// per-install GUID; production wiring supplies it via the
// deps.ConfigProvider narrow interface — see [PlexImport.New]).
// Empty appName / appVersion fall back to ("Sonarr", "5.0") —
// approximating the C# BuildInfo.AppName + BuildInfo.Version.
func NewProxy(doer httpDoer, clientIdentifier, appName, appVersion string, opts ...ProxyOption) *Proxy {
	if doer == nil {
		doer = http.DefaultClient
	}
	if clientIdentifier == "" {
		clientIdentifier = "sonarr"
	}
	if appName == "" {
		appName = "Sonarr"
	}
	if appVersion == "" {
		appVersion = "5.0"
	}
	p := &Proxy{
		doer:               doer,
		clientIdentifier:   clientIdentifier,
		appName:            appName,
		appVersion:         appVersion,
		hostPlexTv:         defaultHostPlexTv,
		hostPlexTvAuth:     defaultHostPlexTvAuth,
		hostPlexTvDiscover: defaultHostPlexTvDiscover,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// GetPinUrl ports `PlexTvService.GetPinUrl` at
// `PlexTvService.cs:35-59`. Builds (but does not execute) the POST
// /api/v2/pins request — the v3 UI executes the POST itself via
// the returned URL + headers. Returns a PinURLResponse the React
// UI launches OAuth from.
func (p *Proxy) GetPinUrl(_ context.Context) PinURLResponse {
	q := url.Values{}
	p.addContextQuery(q)
	q.Set("strong", "true")
	return PinURLResponse{
		Poll: true,
		URL:  p.hostPlexTv + "/api/v2/pins?" + q.Encode(),
		Headers: map[string]string{
			"Accept": "application/json",
		},
	}
}

// GetSignInUrl ports `PlexTvService.GetSignInUrl` at
// `PlexTvService.cs:61-84`. Returns the OAuth redirect URL the
// browser is sent to after the user clicks SignIn.
//
// The C# code uses Url.Segments.Add("hashBang", "#!") because the
// HttpRequestBuilder.Build strips out the "#!" fragment otherwise.
// The Go port inlines the "/auth/hashBang/#!" path directly.
func (p *Proxy) GetSignInUrl(_ context.Context, callbackURL string, pinID int, pinCode string) SignInURLResponse {
	q := url.Values{}
	q.Set("clientID", p.clientIdentifier)
	q.Set("forwardUrl", callbackURL)
	q.Set("code", pinCode)
	q.Set("context[device][product]", p.appName)
	q.Set("context[device][platform]", "Windows")
	q.Set("context[device][platformVersion]", "7")
	q.Set("context[device][version]", p.appVersion)
	return SignInURLResponse{
		OauthURL: p.hostPlexTvAuth + "/auth/hashBang/#!?" + q.Encode(),
		PinID:    pinID,
	}
}

// GetAuthToken ports `PlexTvService.GetAuthToken` (via
// PlexTvProxy.GetAuthToken) at `PlexTvProxy.cs:30-41`. Returns the
// authToken from /api/v2/pins/{pinId}, or empty string when the
// pin is not yet redeemed.
func (p *Proxy) GetAuthToken(ctx context.Context, pinID int) (string, error) {
	q := url.Values{}
	p.addContextQuery(q)
	endpoint := p.hostPlexTv + "/api/v2/pins/" + strconv.Itoa(pinID) + "?" + q.Encode()
	body, err := p.do(ctx, http.MethodGet, endpoint, "")
	if err != nil {
		return "", err
	}
	var resp pinResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		// C# uses TryDeserialize → falls back to empty response.
		// Go port matches by returning empty string + nil err.
		return "", nil
	}
	return resp.AuthToken, nil
}

// Ping ports `PlexTvProxy.Ping` at `PlexTvProxy.cs:43-65`. Issues
// /api/v2/ping with the auth token attached. C# wraps errors as
// trace-level and returns bool; the Go port returns the error so
// callers can surface it.
func (p *Proxy) Ping(ctx context.Context, authToken string) error {
	q := url.Values{}
	p.addContextQuery(q)
	q.Set("X-Plex-Token", authToken)
	endpoint := p.hostPlexTv + "/api/v2/ping?" + q.Encode()
	_, err := p.do(ctx, http.MethodGet, endpoint, "")
	return err
}

// GetWatchlist ports `PlexTvService.GetWatchlist` at
// `PlexTvService.cs:109-138`. Hits the discover.provider.plex.tv
// /library/sections/watchlist/all endpoint with all the
// `context[device][...]` query params + the pagination params.
//
// The C# code calls Ping() before the request; the Go port does
// not (see package doc-comment for rationale).
func (p *Proxy) GetWatchlist(ctx context.Context, authToken string, pageSize, pageOffset int) (WatchlistContainer, error) {
	q := url.Values{}
	q.Set("clientID", p.clientIdentifier)
	q.Set("context[device][product]", p.appName)
	q.Set("context[device][platform]", "Windows")
	q.Set("context[device][platformVersion]", "7")
	q.Set("context[device][version]", p.appVersion)
	q.Set("includeFields", "title,type,year,ratingKey")
	q.Set("excludeElements", "Image")
	q.Set("includeGuids", "1")
	q.Set("sort", "watchlistedAt:desc")
	q.Set("type", "2") // PlexMediaType.Show = 2
	q.Set("X-Plex-Container-Size", strconv.Itoa(pageSize))
	q.Set("X-Plex-Container-Start", strconv.Itoa(pageOffset))
	if authToken != "" {
		q.Set("X-Plex-Token", authToken)
	}
	endpoint := p.hostPlexTvDiscover + "/library/sections/watchlist/all?" + q.Encode()
	body, err := p.do(ctx, http.MethodGet, endpoint, "application/json")
	if err != nil {
		return WatchlistContainer{}, err
	}
	var env watchlistEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return WatchlistContainer{}, fmt.Errorf("%w: decode: %w", ErrRequestFailed, err)
	}
	return env.MediaContainer, nil
}

// addContextQuery adds the X-Plex-* query params every plex.tv
// request carries. Mirrors C# `PlexTvProxy.BuildRequest` at
// `PlexTvProxy.cs:94-106` + the inlined block in
// `PlexTvService.GetPinUrl` at `:41-47`.
func (p *Proxy) addContextQuery(q url.Values) {
	q.Set("X-Plex-Client-Identifier", p.clientIdentifier)
	q.Set("X-Plex-Product", p.appName)
	q.Set("X-Plex-Platform", "Windows")
	q.Set("X-Plex-Platform-Version", "7")
	q.Set("X-Plex-Device-Name", p.appName)
	q.Set("X-Plex-Version", p.appVersion)
}

// do executes a request with the given method/URL/body, returning
// the response bytes. Wraps any transport-level error or non-2xx
// response as [ErrRequestFailed].
func (p *Proxy) do(ctx context.Context, method, endpoint, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build: %w", ErrRequestFailed, err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := p.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: do: %w", ErrRequestFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%w: status %d: %s", ErrRequestFailed, resp.StatusCode, string(excerpt))
	}
	return io.ReadAll(resp.Body)
}
