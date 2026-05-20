package hdbits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

// Indexer is the HDBits Provider impl. C# cite:
// `src/NzbDrone.Core/Indexers/HDBits/HDBits.cs:9-32`.
type Indexer struct {
	HTTPClient httpclient.Client

	definition *provider.Definition
}

// NewIndexer constructs an HDBits indexer.
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
// user must populate Username + ApiKey at Create time.
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
// sibling indexers.
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

// FetchRecent issues the no-filter `/api/torrents` POST. C# cite:
// `HDBitsRequestGenerator.cs:15-22` (GetRecentRequests with empty
// TorrentQuery). The request carries the user's auth + the
// configured Categories/Codecs/Mediums + Limit=100; no search /
// filter params.
func (i *Indexer) FetchRecent(ctx context.Context) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	if i.HTTPClient == nil {
		return nil, errors.New("hdbits: no HTTPClient configured")
	}
	q := newBaseQuery(settings)
	return i.fetchQuery(ctx, settings, q)
}

// FetchSearch returns empty for any criteria. The C# per-criteria
// routing at HDBitsRequestGenerator.cs:24-137 is deferred to a
// follow-up slice — see the package doc-comment.
func (i *Indexer) FetchSearch(_ context.Context, _ any) ([]*parser.TorrentInfo, error) {
	return []*parser.TorrentInfo{}, nil
}

// newBaseQuery builds the default torrentQuery body with auth +
// filter parameters populated. Mirrors C#
// `HDBitsRequestGenerator.GetRequest` at `:152-176` (modulo the
// per-criteria Search / Tvdb / Page tail).
func newBaseQuery(s *Settings) *torrentQuery {
	limit := pageSize
	q := &torrentQuery{
		Username: strings.TrimSpace(s.Username),
		Passkey:  strings.TrimSpace(s.ApiKey),
		Limit:    &limit,
	}
	if len(s.Categories) > 0 {
		q.Category = append([]int(nil), s.Categories...)
	}
	if len(s.Codecs) > 0 {
		q.Codec = append([]int(nil), s.Codecs...)
	}
	if len(s.Mediums) > 0 {
		q.Medium = append([]int(nil), s.Mediums...)
	}
	return q
}

// fetchQuery POSTs the given torrentQuery body to /api/torrents
// and parses the response.
func (i *Indexer) fetchQuery(ctx context.Context, settings *Settings, q *torrentQuery) ([]*parser.TorrentInfo, error) {
	apiURL := strings.TrimRight(strings.TrimSpace(settings.BaseUrl), "/") + "/api/torrents"
	body, err := json.Marshal(q)
	if err != nil {
		return nil, fmt.Errorf("hdbits: marshal query: %w", err)
	}
	req, err := httpclient.NewRequest(apiURL, httpclient.AcceptJSON)
	if err != nil {
		return nil, fmt.Errorf("hdbits: build request: %w", err)
	}
	req.Method = "POST"
	req.Headers.SetContentType("application/json")
	req.SetContentBytes(body)
	resp, err := i.HTTPClient.Post(ctx, req)
	if err != nil {
		return nil, err
	}
	return parseResponse([]byte(resp.Content()), settings, i.bindingName(), i.bindingID())
}

func (i *Indexer) settings() (*Settings, error) {
	if i.definition == nil {
		return nil, errors.New("hdbits: indexer is unbound (Definition not set)")
	}
	if i.definition.Settings == nil {
		return nil, errors.New("hdbits: Definition has nil Settings")
	}
	s, ok := i.definition.Settings.(*Settings)
	if !ok {
		return nil, fmt.Errorf("hdbits: Definition.Settings is %T, want *hdbits.Settings", i.definition.Settings)
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

var _ provider.Provider = (*Indexer)(nil)

// Constructor returns a fresh Settings.
func Constructor() any { return NewSettings() }

func init() {
	if err := provider.Default().Replace(provider.FamilyIndexer,
		provider.Key{Impl: Implementation, Contract: ConfigContract},
		Constructor); err != nil {
		panic(fmt.Errorf("hdbits: register constructor: %w", err))
	}
}
