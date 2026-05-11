package sabnzbd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

// Implementation + ConfigContract are the persisted-row identifiers
// for the SAB download client. Mirrors the C# class names
// `Sabnzbd` + `SabnzbdSettings` — the v3/v4/v5 React UI posts these
// strings in the DownloadClient row's Implementation +
// ConfigContract columns, and the registry keys on the same pair.
const (
	Implementation = "Sabnzbd"
	ConfigContract = "SabnzbdSettings"
)

// Sabnzbd is the [downloadclients.Provider] implementation for the
// SABnzbd usenet client. Ports C# `Sabnzbd` at
// `src/NzbDrone.Core/Download/Clients/Sabnzbd/Sabnzbd.cs:20-594`.
//
// Sub-arc 14a scope: Name / Protocol / Definition / Download / Test
// are live. GetItems / GetStatus / RemoveItem / GetImportItem /
// MarkItemAsImported return [downloadclients.ErrNotImplementedYet]
// (a typed sentinel that the orchestrator treats as a partial
// implementation — log + skip vs hard failure). Sub-arc 14b will
// finish those methods once the C# behavioural shape is replayed
// against recorded test fixtures.
type Sabnzbd struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// New constructs a Sabnzbd Provider against the resolved settings +
// proxy. Used by both the registry [Constructor] and by tests that
// pin a stub HTTPDoer.
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *Sabnzbd {
	if log == nil {
		log = slog.Default()
	}
	return &Sabnzbd{settings: settings, proxy: proxy, def: def, log: log}
}

// Name returns the user-facing label. Mirrors C# `Sabnzbd.Name`
// at `:184`.
func (s *Sabnzbd) Name() string { return "SABnzbd" }

// Protocol returns the protocol family. Mirrors C#
// `UsenetClientBase.Protocol` at `UsenetClientBase.cs:36`.
func (s *Sabnzbd) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolUsenet
}

// Definition returns the persisted DownloadClients row. Mirrors C#
// `DownloadClientBase.Definition` at `:70`.
func (s *Sabnzbd) Definition() *downloadclients.DownloadClient { return s.def }

// Download fires the SAB `addfile` API with the release's NZB
// contents. Mirrors C# `Sabnzbd.AddFromNzbFile` at `:40-53` plus the
// `UsenetClientBase.Download` wrapping at `UsenetClientBase.cs:40-91`.
//
// The Go port skips the C# nzb-validation step (IValidateNzbs.cs)
// — it's a follow-up. The body is whatever the caller hands in via
// req.NzbBody; production callers (queue [15] orchestrator) will
// fetch the URL and pass the bytes here.
//
// Returns the SAB-assigned nzo_id on success, or a wrapped error.
func (s *Sabnzbd) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("sabnzbd: Download: nil request")
	}
	if len(req.NzbBody) == 0 {
		return "", errors.New("sabnzbd: Download: empty NzbBody — fetcher must populate before invoking provider")
	}
	priority := s.settings.RecentTvPriority
	if !req.IsRecent {
		priority = s.settings.OlderTvPriority
	}
	filename := sanitizeFilename(req.ReleaseTitle) + ".nzb"
	id, err := s.proxy.DownloadNzb(ctx, req.NzbBody, filename, s.settings.TvCategory, priority, s.settings)
	if err != nil {
		return "", fmt.Errorf("sabnzbd: Download(%q): %w", req.ReleaseTitle, err)
	}
	return id, nil
}

// GetItems returns the in-flight downloads visible to SAB. Sub-arc
// 14b territory.
func (s *Sabnzbd) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// GetImportItem mirrors C# `Sabnzbd` override at
// `Sabnzbd.cs:447-465`. Sub-arc 14b will port the re-fetch retry
// logic; the C# base default is "return input unchanged".
func (s *Sabnzbd) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	// Default fall-through matches C# `DownloadClientBase.GetImportItem`
	// at `:102-105`. The SAB-specific retry logic is sub-arc 14b.
	return item, nil
}

// RemoveItem routes to queue/delete or history/delete depending on
// the item's current stage. Sub-arc 14b.
func (s *Sabnzbd) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return downloadclients.ErrNotImplementedYet
}

// GetStatus returns the SAB-side configuration snapshot. Sub-arc 14b.
func (s *Sabnzbd) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// MarkItemAsImported moves a SAB completion into the configured
// post-import category. SAB supports this via the history/move
// endpoint; sub-arc 14b will wire it. The default for clients that
// don't support the operation is [downloadclients.ErrMarkAsImportedUnsupported]
// — sub-arc 14b decides which sentinel to return based on the actual
// move support in HEAD's C# code.
func (s *Sabnzbd) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrNotImplementedYet
}

// Test runs the SAB connection + version self-check. Ports the
// minimum-viable portion of C# `Sabnzbd.Test` at `:286-538` — the
// connection-and-version check at `:368-414`. The full Test method
// in C# also checks authentication / global config / category
// existence; those reach into the GetConfig endpoint and are
// sub-arc 14b territory.
//
// Returns the FluentValidation-style failure list (empty = healthy).
// A non-nil error is reserved for "test was aborted due to
// unexpected exception" matching C# `DownloadClientBase.Test`
// outer catch at `:152-159`.
func (s *Sabnzbd) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	var failures []downloadclients.TestFailure

	// First: surface Settings validation failures up front so the
	// user sees them without hitting SAB. Matches C#
	// FluentValidation pipeline ordering at `Test` entry.
	if vFails := s.settings.Validate(); len(vFails) > 0 {
		// Filter out warning-only failures (TvCategory) — the C#
		// pipeline surfaces those as warnings, not blockers. The
		// Go TestFailure shape doesn't yet distinguish; the
		// downstream UI consumer treats the empty-category case
		// as informational. For now, surface all.
		failures = append(failures, vFails...)
	}

	rawVersion, err := s.proxy.GetVersion(ctx, s.settings)
	if err != nil {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        fmt.Sprintf("Unable to connect to SABnzbd: %v", err),
			DetailedDescription: err.Error(),
		})
		return failures, nil
	}

	version := parseVersion(rawVersion)
	if version == nil {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("Unknown SABnzbd version: %s", rawVersion),
		})
		return failures, nil
	}

	// Version-floor check at C# `:389-404`: SAB >= 0.7.0 required.
	// Major>=1 passes; Major==0 && Minor>=7 passes.
	if version.major < 1 && version.minor < 7 {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("SABnzbd %s is too old; required version is 0.7.0 or newer", rawVersion),
		})
	}

	return failures, nil
}

// versionTriple holds a parsed SAB version. Mirrors C# `Version` use
// in `Sabnzbd.ParseVersion` at `:334-366`.
type versionTriple struct {
	major, minor, patch int
}

// versionPattern matches SAB version strings like "3.7.2" or "3.7.x".
// Mirrors C# `VersionRegex` at `Sabnzbd.cs:38` exactly. The "x" suffix
// is the git-build sentinel.
var versionPattern = regexp.MustCompile(`(?P<major>\d+)\.(?P<minor>\d+)\.(?P<patch>\d+|x)`)

// parseVersion ports C# `Sabnzbd.ParseVersion` at `:334-366`. Returns
// nil on the unknown-version path.
func parseVersion(version string) *versionTriple {
	if version == "" {
		return nil
	}
	m := versionPattern.FindStringSubmatch(version)
	if len(m) == 0 {
		// C# `:353-362`: "develop" → synthetic 3.0.0.
		if version == "develop" {
			return &versionTriple{major: 3}
		}
		return nil
	}
	majorIdx := versionPattern.SubexpIndex("major")
	minorIdx := versionPattern.SubexpIndex("minor")
	patchIdx := versionPattern.SubexpIndex("patch")
	major, _ := strconv.Atoi(m[majorIdx])
	minor, _ := strconv.Atoi(m[minorIdx])
	patchStr := m[patchIdx]
	if patchStr == "x" {
		patchStr = "0"
	}
	patch, _ := strconv.Atoi(patchStr)
	return &versionTriple{major: major, minor: minor, patch: patch}
}

// sanitizeFilename strips characters the OS file API + SAB's
// multipart parser don't tolerate from a release title. Mirrors C#
// `FileNameBuilder.CleanFileName` at
// `src/NzbDrone.Core/Organizer/FileNameBuilder.cs` (the small
// subset used by `UsenetClientBase.Download` at
// `UsenetClientBase.cs:43`).
//
// Replacement strategy: <>:"/\|?* → ' '. Square brackets and
// other valid-but-noisy chars are left as-is.
func sanitizeFilename(s string) string {
	bad := `<>:"/\|?*`
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || containsByte(bad, c) {
			out = append(out, ' ')
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

func containsByte(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}

// Compile-time guard: Sabnzbd satisfies the Provider interface.
var _ downloadclients.Provider = (*Sabnzbd)(nil)

// constructor is the registry factory function. Deserialises the
// row's Settings JSON into a [Settings] value, constructs a Proxy
// over deps.HTTPClient (cast to an [HTTPDoer]; production binds
// `*httpclient.Client`), and returns a fully-wired Sabnzbd.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("sabnzbd: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("sabnzbd: settings unmarshal: %w", err)
		}
	}
	doer, ok := deps.HTTPClient.(HTTPDoer)
	if !ok || doer == nil {
		return nil, errors.New("sabnzbd: deps.HTTPClient does not satisfy HTTPDoer")
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	proxy, err := NewProxy(doer, log)
	if err != nil {
		return nil, err
	}
	return New(def, settings, proxy, log), nil
}

// init registers the SAB Provider with the package-global
// [downloadclients.Default] registry. Mirrors the C# DI auto-
// registration pattern (every IDownloadClient implementation is
// auto-wired by the DryIoc container at startup).
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
