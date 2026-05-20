package filelist

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

// Indexer is the FileList Provider impl. C# cite:
// `src/NzbDrone.Core/Indexers/FileList/FileList.cs:9-30`.
type Indexer struct {
	HTTPClient httpclient.Client

	definition *provider.Definition
}

// NewIndexer constructs a FileList indexer.
func NewIndexer(client httpclient.Client) *Indexer {
	return &Indexer{HTTPClient: client}
}

// Name returns the user-visible class name.
func (*Indexer) Name() string { return DisplayName }

// ConfigContract returns the Settings class identifier.
func (*Indexer) ConfigContract() string { return ConfigContract }

// Message returns nil.
func (*Indexer) Message() *provider.Message { return nil }

// DefaultDefinitions returns a single pre-canned definition. The
// user must populate Username + Passkey at Create time.
func (i *Indexer) DefaultDefinitions() []*provider.Definition {
	return []*provider.Definition{
		{
			Name:           DisplayName,
			Implementation: Implementation,
			ConfigContract: ConfigContract,
			Enable:         false,
			Settings:       NewSettings(),
		},
	}
}

// Definition returns the bound Definition.
func (i *Indexer) Definition() *provider.Definition { return i.definition }

// SetDefinition binds the Definition.
func (i *Indexer) SetDefinition(d *provider.Definition) { i.definition = d }

// Test runs Settings.Validate. Same validate-only posture as the
// sibling indexers — no live caps probe.
func (i *Indexer) Test(_ context.Context) provider.ValidationResult {
	settings, err := i.settings()
	if err != nil {
		return provider.ValidationResult{Errors: []provider.ValidationFailure{
			provider.NewValidationFailure("", err.Error()),
		}}
	}
	return settings.Validate()
}

// RequestAction returns nil for everything.
func (i *Indexer) RequestAction(_ string, _ map[string]string) (any, error) {
	return nil, nil
}

// FetchRecent issues the no-search `latest-torrents` API call.
// C# cite: `FileListRequestGenerator.cs:15-22` — single request
// with the Categories+AnimeCategories union, no per-criteria
// parameters.
func (i *Indexer) FetchRecent(ctx context.Context) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	if i.HTTPClient == nil {
		return nil, errors.New("filelist: no HTTPClient configured")
	}
	cats := unionInts(settings.Categories, settings.AnimeCategories)
	if len(cats) == 0 {
		// C# `GetRequest` at :140-143 yield-breaks when categories
		// empty; mirror by returning empty without HTTP.
		return []*parser.TorrentInfo{}, nil
	}
	feedURL := buildAPIURL(settings, "latest-torrents", cats, "")
	return i.fetch(ctx, settings, feedURL)
}

// FetchSearch returns empty for any criteria. The C# per-criteria
// routing at FileListRequestGenerator.cs:24-120 is deferred to a
// follow-up slice — see the package doc-comment.
func (i *Indexer) FetchSearch(_ context.Context, _ any) ([]*parser.TorrentInfo, error) {
	return []*parser.TorrentInfo{}, nil
}

// fetch performs the HTTP request, parses the response, and stamps
// indexer provenance onto each TorrentInfo. Used by FetchRecent
// (single URL) and -- in a follow-up slice -- FetchSearch (per-
// criteria URL list).
func (i *Indexer) fetch(ctx context.Context, settings *Settings, feedURL string) ([]*parser.TorrentInfo, error) {
	req, err := httpclient.NewRequest(feedURL, httpclient.AcceptJSON)
	if err != nil {
		return nil, fmt.Errorf("filelist: build request: %w", err)
	}
	req.BasicAuthUser = strings.TrimSpace(settings.Username)
	req.BasicAuthPass = strings.TrimSpace(settings.Passkey)
	resp, err := i.HTTPClient.Get(ctx, req)
	if err != nil {
		return nil, err
	}
	return parseResponse([]byte(resp.Content()), settings, i.bindingName(), i.bindingID())
}

// buildAPIURL composes the FileList API URL. Mirrors C#
// `FileListRequestGenerator.cs:138-153` — `<BaseUrl>/api.php?action=<...>&category=<csv>[<parameters>]`.
//
// `parameters` is the per-criteria extra query string (already
// `&`-prefixed when non-empty — mirrors the C# format string).
func buildAPIURL(s *Settings, action string, categories []int, parameters string) string {
	base := strings.TrimRight(strings.TrimSpace(s.BaseUrl), "/")
	catCSV := joinInts(categories, ",")
	u := base + "/api.php?action=" + action + "&category=" + catCSV + parameters
	return u
}

// unionInts concatenates two []int slices and dedupes, preserving
// the order of first appearance. Mirrors C#
// `Categories.Concat(AnimeCategories)` followed by the
// `GetRequest`-time `Distinct()` at :145.
func unionInts(a, b []int) []int {
	seen := make(map[int]struct{}, len(a)+len(b))
	out := make([]int, 0, len(a)+len(b))
	for _, v := range a {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, v := range b {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// joinInts builds a comma-separated string from a []int. Used for
// the `category=<csv>` query param.
func joinInts(xs []int, sep string) string {
	if len(xs) == 0 {
		return ""
	}
	parts := make([]string, len(xs))
	for i, v := range xs {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, sep)
}

func (i *Indexer) settings() (*Settings, error) {
	if i.definition == nil {
		return nil, errors.New("filelist: indexer is unbound (Definition not set)")
	}
	if i.definition.Settings == nil {
		return nil, errors.New("filelist: Definition has nil Settings")
	}
	s, ok := i.definition.Settings.(*Settings)
	if !ok {
		return nil, fmt.Errorf("filelist: Definition.Settings is %T, want *filelist.Settings", i.definition.Settings)
	}
	return s, nil
}

func (i *Indexer) bindingName() string {
	if i.definition != nil && i.definition.Name != "" {
		return i.definition.Name
	}
	return i.Name()
}

func (i *Indexer) bindingID() int {
	if i.definition == nil {
		return 0
	}
	return i.definition.ID
}

// Compile-time interface check.
var _ provider.Provider = (*Indexer)(nil)

// Constructor returns a fresh Settings.
func Constructor() any { return NewSettings() }

func init() {
	if err := provider.Default().Replace(provider.FamilyIndexer,
		provider.Key{Impl: Implementation, Contract: ConfigContract},
		Constructor); err != nil {
		panic(fmt.Errorf("filelist: register constructor: %w", err))
	}
}
