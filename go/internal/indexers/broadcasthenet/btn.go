package broadcasthenet

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

// Indexer is the BroadcastheNet Provider impl. C# cite:
// `src/NzbDrone.Core/Indexers/BroadcastheNet/BroadcastheNet.cs:10-43`.
type Indexer struct {
	HTTPClient httpclient.Client

	definition *provider.Definition
}

// NewIndexer constructs a BroadcastheNet indexer.
func NewIndexer(client httpclient.Client) *Indexer {
	return &Indexer{HTTPClient: client}
}

// Name returns the user-visible class name.
func (*Indexer) Name() string { return DisplayName }

// ConfigContract returns the Settings class identifier.
func (*Indexer) ConfigContract() string { return ConfigContract }

// Message returns nil.
func (*Indexer) Message() *provider.Message { return nil }

// DefaultDefinitions returns a single pre-canned definition.
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

// FetchRecent issues paginated `getTorrents` JSON-RPC calls with
// `Age=<=86400` (24-hour window). C# cite:
// `BroadcastheNetRequestGenerator.cs:22-40` (the second tier; the
// LastRecentTorrentId optimization at `:26-32` is deferred to a
// follow-up slice once IIndexerStatusService is wired into the
// search-time deps).
//
// Walks up to [maxPages] pages, Guid-dedupes across pages, and
// short-circuits as soon as a page returns zero new torrents.
func (i *Indexer) FetchRecent(ctx context.Context) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	if i.HTTPClient == nil {
		return nil, errors.New("broadcasthenet: no HTTPClient configured")
	}
	return i.fetchPaged(ctx, settings, &torrentQuery{Age: "<=86400"})
}

// FetchSearch returns empty for any criteria. The C# per-criteria
// routing at BroadcastheNetRequestGenerator.cs:42-225 is deferred
// to a follow-up slice — see the package doc-comment.
func (i *Indexer) FetchSearch(_ context.Context, _ any) ([]*parser.TorrentInfo, error) {
	return []*parser.TorrentInfo{}, nil
}

// fetchPaged walks the pagination loop. Mirrors C#
// `GetPagedRequests` at `:245-257` — increments `params[3]` (offset)
// by PageSize across MaxPages iterations.
func (i *Indexer) fetchPaged(ctx context.Context, settings *Settings, q *torrentQuery) ([]*parser.TorrentInfo, error) {
	dedup := make(map[string]struct{}, maxPages*pageSize)
	var all []*parser.TorrentInfo
	for page := 0; page < maxPages; page++ {
		offset := page * pageSize
		rels, err := i.callRPC(ctx, settings, q, offset)
		if err != nil {
			// Per-page failures swallowed — matches the C# tree's
			// per-page try/catch. The accumulated results from prior
			// pages still surface.
			break
		}
		newCount := 0
		for _, r := range rels {
			if _, dup := dedup[r.Guid]; dup {
				continue
			}
			dedup[r.Guid] = struct{}{}
			all = append(all, r)
			newCount++
		}
		// Empty page (or all-duplicates) → no more pages to walk.
		if newCount == 0 {
			break
		}
	}
	if all == nil {
		all = []*parser.TorrentInfo{}
	}
	return all, nil
}

// callRPC issues one JSON-RPC POST and parses the response.
func (i *Indexer) callRPC(ctx context.Context, settings *Settings, q *torrentQuery, offset int) ([]*parser.TorrentInfo, error) {
	apiURL := strings.TrimRight(strings.TrimSpace(settings.BaseUrl), "/")
	rpcReq := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "getTorrents",
		Params:  []any{strings.TrimSpace(settings.ApiKey), q, pageSize, offset},
		ID:      1,
	}
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("broadcasthenet: marshal RPC request: %w", err)
	}
	req, err := httpclient.NewRequest(apiURL, httpclient.AcceptJSON)
	if err != nil {
		return nil, fmt.Errorf("broadcasthenet: build request: %w", err)
	}
	req.Method = "POST"
	req.Headers.SetContentType("application/json")
	req.SetContentBytes(body)
	resp, err := i.HTTPClient.Post(ctx, req)
	if err != nil {
		return nil, err
	}
	contentType := ""
	if resp.Headers != nil {
		contentType = resp.Headers.ContentType()
	}
	scheme := "https"
	if strings.HasPrefix(apiURL, "http:") {
		scheme = "http"
	}
	return parseResponse(
		[]byte(resp.Content()),
		resp.StatusCode,
		contentType,
		scheme,
		settings,
		i.bindingName(),
		i.bindingID(),
	)
}

func (i *Indexer) settings() (*Settings, error) {
	if i.definition == nil {
		return nil, errors.New("broadcasthenet: indexer is unbound (Definition not set)")
	}
	if i.definition.Settings == nil {
		return nil, errors.New("broadcasthenet: Definition has nil Settings")
	}
	s, ok := i.definition.Settings.(*Settings)
	if !ok {
		return nil, fmt.Errorf("broadcasthenet: Definition.Settings is %T, want *broadcasthenet.Settings", i.definition.Settings)
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
		panic(fmt.Errorf("broadcasthenet: register constructor: %w", err))
	}
}
