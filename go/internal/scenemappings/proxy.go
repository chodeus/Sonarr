package scenemappings

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

// proxy.go ports `SceneMappingProxy` at
// `src/NzbDrone.Core/DataAugmentation/Scene/SceneMappingProxy.cs:12-32`.
//
// Slice 4.AX (sub-arc M6a of queue [M6]) — the only concrete
// [Provider] in the C# tree. Hits the services.sonarr.tv cloud
// endpoint and returns the raw mapping list; the diff-merge + cache
// refresh happens upstream in [Service.UpdateMappings] (sub-arc M6b).
//
// Wire-format note: the cloud endpoint emits `[JsonProperty]`-renamed
// fields per the C# `SceneMapping` class — `searchTitle` (not
// `searchTerm`) and `season` (not `seasonNumber`). The Go port decodes
// via [sceneMappingResource], a private inbound DTO whose JSON tags
// match the cloud wire shape. The DTO is then mapped to the public
// [Mapping] struct.
//
// The public [Mapping] struct's existing JSON tags (`"searchTerm"` +
// `"seasonNumber"`) are inherited from earlier slice work and do NOT
// match the cloud wire shape. They are flagged as Category-2 drift in
// go/JSON_CONVENTIONS.md's known-non-compliant list; the wider Option-A
// sweep slice will reconcile them. This sub-arc isolates the
// reconciliation by using the private DTO for the inbound parse only.

// ServicesSceneMappingURL is the canonical cloud endpoint per C#
// `SceneMappingProxy.cs:25-27` (`Resource("/scenemapping")` against
// `ISonarrCloudRequestBuilder.Services` which has base
// `https://services.sonarr.tv/v1/`). Exported so the M6b main.go
// wiring imports a single source of truth rather than re-typing the
// literal at the boot site.
const ServicesSceneMappingURL = "https://services.sonarr.tv/v1/scenemapping"

// Proxy is the SceneMappingProvider that fetches mappings from the
// Servarr-hosted services.sonarr.tv cloud endpoint.
//
// Goroutine-safety: stateless after construction. The underlying
// *http.Client is goroutine-safe by net/http contract.
type Proxy struct {
	httpClient *http.Client
	baseURL    string
	log        *slog.Logger
}

// NewProxy constructs a Proxy. nil httpClient or empty baseURL is a
// programmer error; nil log defaults to slog.Default().
//
// baseURL is the full resource URL (e.g.
// `https://services.sonarr.tv/v1/scenemapping`) — taking the
// resource-level URL keeps the port consistent with the C# pattern
// where `_requestBuilder.Create().Resource("/scenemapping").Build()`
// produces the same fixed URL on every call. A future config-driven
// override slice would substitute a different baseURL.
func NewProxy(httpClient *http.Client, baseURL string, log *slog.Logger) (*Proxy, error) {
	if httpClient == nil {
		return nil, errors.New("scenemappings: NewProxy: httpClient is nil")
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("scenemappings: NewProxy: baseURL is empty")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{
		httpClient: httpClient,
		baseURL:    baseURL,
		log:        log,
	}, nil
}

// GetSceneMappings ports `SceneMappingProxy.Fetch()` at C#
// `SceneMappingProxy.cs:23-30`. Issues a GET against the cloud
// endpoint, decodes the response as `[]sceneMappingResource`, and
// maps each entry into a [Mapping] with the correct field
// translations.
//
// Errors: any transport, status-code, or decode failure surfaces as
// a wrapped error. The C# code lets the IHttpClient.Get<T> throw and
// the upstream [SceneMappingService.UpdateMappings] catches at
// `:208-211` and logs at Error level. The Go port returns the
// wrapped error; the consuming UpdateMappings (sub-arc M6b) is
// responsible for the catch-and-log behaviour mirroring C# parity.
//
// ctx.Err() propagates verbatim so callers can distinguish
// "cancelled" from "cloud unavailable".
func (p *Proxy) GetSceneMappings(ctx context.Context) ([]Mapping, error) {
	if p == nil {
		return nil, errors.New("scenemappings: Proxy is nil")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("scenemappings: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("scenemappings: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("scenemappings: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("scenemappings: read body: %w", err)
	}

	var resources []sceneMappingResource
	if err := json.Unmarshal(body, &resources); err != nil {
		return nil, fmt.Errorf("scenemappings: decode: %w", err)
	}

	out := make([]Mapping, 0, len(resources))
	for i := range resources {
		out = append(out, resources[i].toMapping())
	}
	return out, nil
}

// sceneMappingResource is the wire-format DTO that mirrors the C#
// `SceneMapping` JSON shape at `SceneMapping.cs:6-29` (specifically
// honouring the `[JsonProperty("searchTitle")]` + `[JsonProperty("season")]`
// renames).
//
// Field-for-field mapping to [Mapping]:
//
//	C# wire             Go DTO field    Go DTO json tag    → Mapping field
//	mappingId           MappingID       "mappingId"        → MappingID
//	title               Title           "title"            → Title
//	parseTerm           ParseTerm       "parseTerm"        → ParseTerm (NOTE: cloud doesn't always emit this; UpdateMappings computes it)
//	searchTitle         SearchTerm      "searchTitle"      → SearchTerm    (RENAME)
//	tvdbId              TvdbID          "tvdbId"           → TvdbID
//	season              SeasonNumber    "season"           → SeasonNumber  (RENAME, nullable)
//	sceneSeasonNumber   SceneSeasonNumber "sceneSeasonNumber" → SceneSeasonNumber (nullable)
//	sceneOrigin         SceneOrigin     "sceneOrigin"      → SceneOrigin
//	searchMode          SearchMode      "searchMode"       → SearchMode    (nullable enum-as-int)
//	comment             Comment         "comment"          → Comment
//	filterRegex         FilterRegex     "filterRegex"      → FilterRegex
//	type                Type            "type"             → Type          (NOTE: cloud doesn't emit this; UpdateMappings stamps it from providerType)
//
// Nullable handling: `int?` on C# → `*int` on Go for SeasonNumber,
// SceneSeasonNumber, SearchMode. Non-nullable `int` on TvdbId. Strings
// passed through as plain strings.
type sceneMappingResource struct {
	MappingID         string `json:"mappingId"`
	Title             string `json:"title"`
	ParseTerm         string `json:"parseTerm"`
	SearchTerm        string `json:"searchTitle"` // C# [JsonProperty("searchTitle")]
	TvdbID            int    `json:"tvdbId"`
	SeasonNumber      *int   `json:"season"` // C# [JsonProperty("season")]
	SceneSeasonNumber *int   `json:"sceneSeasonNumber"`
	SceneOrigin       string `json:"sceneOrigin"`
	SearchMode        *int   `json:"searchMode"`
	Comment           string `json:"comment"`
	FilterRegex       string `json:"filterRegex"`
	Type              string `json:"type"`
}

// toMapping converts the wire-format DTO into a persistence-shaped
// [Mapping]. Fields are copied verbatim; the upstream
// [Service.UpdateMappings] (sub-arc M6b) re-computes ParseTerm via
// parser.CleanSeriesTitle(Title) and stamps Type from the
// providerType name.
func (r *sceneMappingResource) toMapping() Mapping {
	return Mapping{
		MappingID:         r.MappingID,
		Title:             r.Title,
		ParseTerm:         r.ParseTerm,
		SearchTerm:        r.SearchTerm,
		TvdbID:            r.TvdbID,
		SeasonNumber:      r.SeasonNumber,
		SceneSeasonNumber: r.SceneSeasonNumber,
		SceneOrigin:       r.SceneOrigin,
		SearchMode:        r.SearchMode,
		Comment:           r.Comment,
		FilterRegex:       r.FilterRegex,
		Type:              r.Type,
	}
}

// Compile-time guard. Proxy implements [Provider].
var _ Provider = (*Proxy)(nil)
