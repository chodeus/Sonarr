package sonarr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"sonarr.tv/sonarr/internal/importlists"
)

// httpDoer is the minimal HTTP-client surface the proxy needs.
// Pattern-mirror of the [internal/notifications/webhook.httpDoer]
// precedent at `internal/notifications/webhook/proxy.go:18`. Kept
// tiny so tests can swap in an httptest.Server-backed client
// without dragging the full *httpclient.Client surface through.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrRequestFailed wraps any transport-level failure (DNS, TCP,
// non-2xx response). Mirrors C# `HttpException` raised by
// IHttpClient inside `SonarrV3Proxy.Execute` at
// `SonarrV3Proxy.cs:109-112` — the C# code wraps any non-2xx
// response as an HttpException; the Go port wraps any transport-
// level error or non-2xx response as [ErrRequestFailed].
var ErrRequestFailed = errors.New("sonarr-import: request failed")

// httpError is the internal classified-error type the proxy returns
// from Execute. It carries the HTTP status code (0 for transport
// errors) so the Test() entrypoint can branch on
// Unauthorized/redirect/other per the C# Test path at
// `SonarrV3Proxy.cs:61-89`.
type httpError struct {
	StatusCode int
	Redirect   bool
	Cause      error
}

func (e *httpError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("sonarr-import: status=%d redirect=%v: %v", e.StatusCode, e.Redirect, e.Cause)
	}
	return fmt.Sprintf("sonarr-import: status=%d redirect=%v", e.StatusCode, e.Redirect)
}

func (e *httpError) Unwrap() error { return ErrRequestFailed }

// Proxy is the HTTP wrapper for the remote Sonarr /api/v3 endpoints.
// Mirrors C# `SonarrV3Proxy` at
// `src/NzbDrone.Core/ImportLists/Sonarr/SonarrV3Proxy.cs:23-118`.
//
// Concurrency: the proxy is stateless — every method reads settings
// + does a single GET against the peer. Safe for concurrent use.
type Proxy struct {
	doer httpDoer
}

// NewProxy binds an httpDoer into a Proxy. nil doer falls back to
// [http.DefaultClient] (matches the C# DI default — SystemNet
// HttpClient is always available via the IHttpClient resolver).
func NewProxy(doer httpDoer) *Proxy {
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Proxy{doer: doer}
}

// GetSeries ports `SonarrV3Proxy.GetSeries` at
// `SonarrV3Proxy.cs:36-39`. Hits /api/v3/series.
func (p *Proxy) GetSeries(ctx context.Context, settings *Settings) ([]Series, error) {
	return execute[Series](ctx, p.doer, settings, "/api/v3/series")
}

// GetQualityProfiles ports `SonarrV3Proxy.GetQualityProfiles` at
// `SonarrV3Proxy.cs:41-44`. Hits /api/v3/qualityprofile.
func (p *Proxy) GetQualityProfiles(ctx context.Context, settings *Settings) ([]Profile, error) {
	return execute[Profile](ctx, p.doer, settings, "/api/v3/qualityprofile")
}

// GetLanguageProfiles ports `SonarrV3Proxy.GetLanguageProfiles` at
// `SonarrV3Proxy.cs:46-49`. Hits /api/v3/languageprofile.
//
// Modern Sonarr v3 removed language profiles; this endpoint returns
// 404 on any current peer. The C# code still queries it (the UI's
// "Language Profiles" Select field will be empty). Port mirrors
// exactly — caller is expected to tolerate an empty result.
func (p *Proxy) GetLanguageProfiles(ctx context.Context, settings *Settings) ([]Profile, error) {
	return execute[Profile](ctx, p.doer, settings, "/api/v3/languageprofile")
}

// GetRootFolders ports `SonarrV3Proxy.GetRootFolders` at
// `SonarrV3Proxy.cs:51-54`. Hits api/v3/rootfolder (note: no
// leading slash in the C# original — mirrored verbatim; the
// HttpRequestBuilder.Resource normalises either form).
func (p *Proxy) GetRootFolders(ctx context.Context, settings *Settings) ([]RootFolder, error) {
	return execute[RootFolder](ctx, p.doer, settings, "/api/v3/rootfolder")
}

// GetTags ports `SonarrV3Proxy.GetTags` at
// `SonarrV3Proxy.cs:56-59`. Hits /api/v3/tag.
func (p *Proxy) GetTags(ctx context.Context, settings *Settings) ([]Tag, error) {
	return execute[Tag](ctx, p.doer, settings, "/api/v3/tag")
}

// Test ports `SonarrV3Proxy.Test` at
// `SonarrV3Proxy.cs:61-91`. Issues a GetSeries against the peer
// and maps the outcome to a single [importlists.TestFailure]:
//
//   - HTTP 401 → ApiKey failure ("API Key is invalid").
//   - HTTP 3xx redirect → BaseUrl failure ("Sonarr returned redirect
//     and is invalid"). C# checks `response.HasHttpRedirect`; we
//     mirror by detecting StatusCode in [300,400).
//   - Any other HttpException / transport error → generic
//     "Unable to connect to import list: <message>" with PropertyName="".
//   - Success → nil (no failures).
//
// Returns ([]TestFailure, error) — error is reserved for unexpected
// panics from the JSON layer; the caller's importlists.Provider.Test
// surface always returns an empty failure slice on success.
func (p *Proxy) Test(ctx context.Context, settings *Settings) []importlists.TestFailure {
	_, err := p.GetSeries(ctx, settings)
	if err == nil {
		return nil
	}
	var he *httpError
	if errors.As(err, &he) {
		switch {
		case he.StatusCode == http.StatusUnauthorized:
			return []importlists.TestFailure{{
				PropertyName: "ApiKey",
				ErrorMessage: "API Key is invalid.",
			}}
		case he.Redirect:
			return []importlists.TestFailure{{
				PropertyName: "BaseUrl",
				ErrorMessage: "Sonarr returned redirect and is invalid.",
			}}
		}
	}
	return []importlists.TestFailure{{
		PropertyName: "",
		ErrorMessage: fmt.Sprintf("Unable to connect to import list: %v", err),
	}}
}

// execute is the generic implementation of C# `Execute<TResource>`
// at `SonarrV3Proxy.cs:93-117`. Mirrors the C# Empty-settings short-
// circuit, the trailing-slash trim, the X-Api-Key header, the
// `>= 300` failure threshold, and the JSON deserialisation.
//
// Settings nil or BaseURL/APIKey blank → returns empty slice + nil
// error (mirrors C# `:95-97`: empty BaseUrl/ApiKey returns
// `new List<TResource>()` without making a request).
func execute[T any](ctx context.Context, doer httpDoer, settings *Settings, resource string) ([]T, error) {
	if settings == nil ||
		strings.TrimSpace(settings.BaseURL) == "" ||
		strings.TrimSpace(settings.APIKey) == "" {
		return []T{}, nil
	}

	baseURL := strings.TrimRight(settings.BaseURL, "/")
	// Join base + resource. The C# HttpRequestBuilder.Resource
	// normalises a leading-slash-or-not; we do the same by ensuring
	// exactly one slash separates them.
	if !strings.HasPrefix(resource, "/") {
		resource = "/" + resource
	}
	fullURL, err := url.Parse(baseURL + resource)
	if err != nil {
		return nil, &httpError{Cause: fmt.Errorf("parse url: %w", err)}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL.String(), nil)
	if err != nil {
		return nil, &httpError{Cause: fmt.Errorf("build request: %w", err)}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Api-Key", settings.APIKey)

	resp, err := doer.Do(req)
	if err != nil {
		return nil, &httpError{Cause: fmt.Errorf("do: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		// Read a small body excerpt for the error message; mirrors
		// the webhook proxy's 512-byte cap.
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &httpError{
			StatusCode: resp.StatusCode,
			Redirect:   resp.StatusCode >= 300 && resp.StatusCode < 400,
			Cause:      fmt.Errorf("status %d: %s", resp.StatusCode, string(excerpt)),
		}
	}

	var out []T
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&out); err != nil {
		return nil, &httpError{Cause: fmt.Errorf("decode: %w", err)}
	}
	return out, nil
}

// isValidRootURL is the Go port of
// `NzbDrone.Common.Extensions.UrlValidation.IsValidUrl` filtered
// for the "ValidRootUrl" FluentValidation rule applied to BaseUrl
// at `SonarrSettings.cs:13`. Requires:
//
//   - parses as a URL,
//   - has http or https scheme,
//   - has a non-empty host.
//
// Trailing slashes and paths are allowed (the proxy strips trailing
// slashes at request-build time). Lives in proxy.go because the
// Settings file kept its scope tight to the Settings struct +
// Validate; the helper is consumed by Settings.Validate.
func isValidRootURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}
