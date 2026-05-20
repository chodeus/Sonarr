// Package update ports a subset of `NzbDrone.Core.Update`. Slice
// 5.J40 lands the read-only update-check side:
//
//   - [Package] mirrors `Update/UpdatePackage.cs:5-15`.
//   - [Service.AvailableUpdate] mirrors
//     `Update/CheckUpdateService.cs:13-30` + the GetLatestUpdate
//     fetch at `Update/UpdatePackageProvider.cs:24-58`.
//
// The install-side (`InstallUpdateService`, `ApplicationUpdateCommand`,
// `Execute(ApplicationUpdateCommand)`) is intentionally NOT ported —
// Sonarr-Go runs only the read-side healthcheck + `ApplicationCheckUpdate`
// command today. When the install pipeline ports, this package gains
// `Install(pkg)` + the writability + GPG-verify checks; the command
// chain remains the same shape.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

// DefaultServicesBaseURL is the upstream feed root. Mirrors C#
// `SonarrCloudRequestBuilder.cs:15` — `https://services.sonarr.tv/v1/`.
// Production wiring passes this to [NewService]; tests pass an
// httptest server URL.
const DefaultServicesBaseURL = "https://services.sonarr.tv/v1"

// Package is the per-release projection the upstream feed returns.
// Mirrors C# `UpdatePackage` at `Update/UpdatePackage.cs:5-15`. The
// `Changes` field (changelog blob — C# UpdateChanges) is omitted from
// the Go port for now — the healthcheck only consumes Version; the
// command handler only consumes the existence of a non-nil package.
type Package struct {
	Version     string    `json:"version"`
	ReleaseDate time.Time `json:"releaseDate"`
	FileName    string    `json:"fileName"`
	URL         string    `json:"url"`
	Hash        string    `json:"hash"`
	Branch      string    `json:"branch"`
}

// availableResponse mirrors the C# `UpdatePackageAvailable` envelope.
// The upstream services.sonarr.tv response wraps the package in an
// `available` flag + `updatePackage` object so the client can
// short-circuit when no update exists.
type availableResponse struct {
	Available     bool     `json:"available"`
	UpdatePackage *Package `json:"updatePackage,omitempty"`
}

// Doer is the slice of [http.Client] the [Service] depends on. The
// `*http.Client` from the stdlib satisfies it; tests can pass a
// stub.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// BranchProvider returns the configured update branch. Production
// wiring binds `*configfile.Provider.Branch(ctx)` via a small
// adapter (the configfile method returns `(string, error)` so the
// adapter discards the error and falls back to the C# default
// "main" when the read fails — matches the C# `_configFileProvider.
// Branch` getter which can't fail).
type BranchProvider interface {
	Branch(ctx context.Context) string
}

// Service ports `CheckUpdateService` at `:13-30`. Goroutine-safe:
// stateless beyond construction-time deps.
type Service struct {
	http    Doer
	branch  BranchProvider
	version string
	baseURL string
	timeout time.Duration
}

// NewService binds the HTTP doer + branch provider + the current
// build's version string. `baseURL` defaults to
// [DefaultServicesBaseURL] when empty; `timeout` defaults to 10s
// when zero.
//
// All parameters are required EXCEPT baseURL + timeout; nil for the
// other three is rejected.
func NewService(httpDoer Doer, branch BranchProvider, version, baseURL string, timeout time.Duration) (*Service, error) {
	if httpDoer == nil {
		return nil, errors.New("update: nil http Doer")
	}
	if branch == nil {
		return nil, errors.New("update: nil BranchProvider")
	}
	if strings.TrimSpace(version) == "" {
		return nil, errors.New("update: empty version")
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultServicesBaseURL
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Service{
		http:    httpDoer,
		branch:  branch,
		version: version,
		baseURL: baseURL,
		timeout: timeout,
	}, nil
}

// AvailableUpdate mirrors `CheckUpdateService.AvailableUpdate` at
// `:25-29`. Hits `<base>/update/{branch}?version=&os=&arch=&runtime=`
// and returns the parsed package when `available == true`, or nil
// when no update is offered. The runtime= param is hardcoded to
// `netcore` to match the C# parity expectation on the
// services.sonarr.tv side (the upstream server differentiates
// netcore vs the legacy mono build via this param; the Go port
// passes through netcore until a Go-specific runtime tag lands on
// the server).
//
// HTTP failures (non-2xx, network errors) return an error; a 2xx
// response with `available: false` returns (nil, nil).
func (s *Service) AvailableUpdate(ctx context.Context) (*Package, error) {
	if s == nil {
		return nil, nil
	}
	br := strings.TrimSpace(s.branch.Branch(ctx))
	if br == "" {
		br = "main"
	}

	q := url.Values{}
	q.Set("version", s.version)
	q.Set("os", goosToServiceOs(runtime.GOOS))
	q.Set("arch", runtime.GOARCH)
	q.Set("runtime", "netcore")
	q.Set("includeMajorVersion", "true")

	u := fmt.Sprintf("%s/update/%s?%s", strings.TrimRight(s.baseURL, "/"), url.PathEscape(br), q.Encode())

	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("update: build request: %w", err)
	}

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: GET %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("update: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed availableResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("update: decode body: %w", err)
	}
	if !parsed.Available {
		return nil, nil
	}
	return parsed.UpdatePackage, nil
}

// goosToServiceOs maps the Go `runtime.GOOS` value to the C#
// `OsInfo.Os.ToString().ToLowerInvariant()` value the upstream
// services.sonarr.tv server expects. The C# enum names ("Linux",
// "Osx", "Windows") differ from the Go GOOS values ("linux",
// "darwin", "windows"); the upstream API lower-cases them.
func goosToServiceOs(goos string) string {
	switch goos {
	case "darwin":
		return "osx"
	case "linux":
		return "linux"
	case "windows":
		return "windows"
	case "freebsd":
		// C# `OsInfo` doesn't surface a BSD variant; the upstream
		// server tolerates an unknown lowercase string. Pass through
		// so an operator's logs surface the actual OS.
		return "freebsd"
	default:
		return goos
	}
}
