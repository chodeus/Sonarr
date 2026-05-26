package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/releasecache"
)

// ReleaseDispatcher is the narrow dispatch interface for both write
// handlers. Identical shape to QueueGrabDispatcher (J236) — separate
// type alias so the two handler families can be wired independently
// without one's interface changing forcing the other to update.
type ReleaseDispatcher interface {
	ProcessDecisions(ctx context.Context, decisions []decisionengine.Decision) *download.ProcessedDecisions
}

// ReleaseDecisionMaker is the narrow decision-engine interface for
// POST /release/push. `*decisionengine.DownloadDecisionMaker` satisfies
// it. The push handler builds a synthetic ReleaseInfo from the body
// and runs the same GetRssDecision path the RSS-sync handler uses,
// matching C# `ReleasePushController.Create` at `:65-74`.
type ReleaseDecisionMaker interface {
	GetRssDecision(ctx context.Context, reports []any, pushedRelease bool) []decisionengine.Decision
}

// ReleasePushBody is the JSON body shape for POST /api/v3/release/push.
// Mirrors the subset of C# `ReleaseResource` that the push controller
// reads — Title, DownloadUrl, MagnetUrl, Indexer, IndexerId, Protocol,
// PublishDate, plus optional DownloadClient routing fields.
//
// PostValidator rules (C# `:42-47`):
//   - Title NotEmpty
//   - DownloadUrl NotEmpty WHEN MagnetUrl is empty
//   - MagnetUrl NotEmpty WHEN DownloadUrl is empty
//   - Protocol NotEmpty
//   - PublishDate NotEmpty
type ReleasePushBody struct {
	Guid             string                    `json:"guid"`
	Title            string                    `json:"title"`
	DownloadURL      string                    `json:"downloadUrl"`
	MagnetURL        string                    `json:"magnetUrl"`
	Indexer          string                    `json:"indexer"`
	IndexerID        int                       `json:"indexerId"`
	Protocol         indexers.DownloadProtocol `json:"protocol"`
	PublishDate      time.Time                 `json:"publishDate"`
	Size             int64                     `json:"size"`
	InfoURL          string                    `json:"infoUrl"`
	CommentURL       string                    `json:"commentUrl"`
	DownloadClient   string                    `json:"downloadClient"`
	DownloadClientID int                       `json:"downloadClientId"`
}

// ReleaseDownloadBody is the JSON body shape for POST /api/v3/release.
// Mirrors the subset of C# `ReleaseResource` that the cache-lookup +
// dispatch path reads.
//
// PostValidator rules (C# `:63-64`):
//   - IndexerId > 0
//   - Guid NotEmpty
//
// ShouldOverride sub-fields (C# `:84-91`):
//   - SeriesId, EpisodeIds, Quality, Languages — all required when
//     ShouldOverride is true.
type ReleaseDownloadBody struct {
	Guid             string          `json:"guid"`
	IndexerID        int             `json:"indexerId"`
	DownloadClient   string          `json:"downloadClient"`
	DownloadClientID int             `json:"downloadClientId"`
	ShouldOverride   bool            `json:"shouldOverride"`
	SeriesID         int             `json:"seriesId"`
	EpisodeID        int             `json:"episodeId"`
	EpisodeIDs       []int           `json:"episodeIds"`
	Quality          json.RawMessage `json:"quality"`
	Languages        json.RawMessage `json:"languages"`
}

// ReleaseDownloadHandler returns POST /api/v3/release.
//
// Mirrors `ReleaseController.DownloadRelease` at C#
// `src/Sonarr.Api.V3/Indexers/ReleaseController.cs:69-169`:
//
//  1. Look up the requested release in the in-memory cache (keyed by
//     `<indexerId>_<guid>` — the cache was populated when the UI
//     called GET /release/search earlier).
//  2. If cache miss → 404 "Couldn't find requested release in cache,
//     try searching again". This is the dominant outcome whenever the
//     30-minute TTL expired or the user came back to Sonarr after a
//     restart; the UI surface re-runs the search to repopulate.
//  3. On cache hit: dispatch the cached Decision via the existing
//     ProcessDecisions orchestrator. Body fields like ShouldOverride
//     + SeriesId + EpisodeIds + Quality + Languages are read for the
//     manual-override branch but the Subject mutation is DEFERRED in
//     this initial port — the cached Decision's Subject is dispatched
//     verbatim. Override surface lands in a follow-up slice (see
//     handler doc-comment).
//  4. Return the same body back to the caller (matches C# `return release`).
//
// Audit row C.1.i-follow-5a (J237). Override-branch port deferred.
func ReleaseDownloadHandler(cache *releasecache.Cache, dispatcher ReleaseDispatcher) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body ReleaseDownloadBody
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("release: malformed body: %v", err))
			return
		}
		// PostValidator (C# `:63-64`).
		failures := []httperr.Failure{}
		if body.IndexerID <= 0 {
			failures = append(failures, httperr.Failure{PropertyName: "IndexerId", ErrorMessage: "'Indexer Id' must be greater than '0'.", Severity: "error"})
		}
		if body.Guid == "" {
			failures = append(failures, httperr.Failure{PropertyName: "Guid", ErrorMessage: "'Guid' must not be empty.", Severity: "error"})
		}
		if len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		key := releasecache.Key(body.IndexerID, body.Guid)
		decision, ok := cache.Get(key)
		if !ok {
			httperr.NotFound(w, "Couldn't find requested release in cache, try searching again")
			return
		}
		// The C# `ShouldOverride==true` arm mutates the cached
		// RemoteEpisode (clones, sets Series/Episodes/Quality/Languages
		// from the request body). DEFERRED in this port — Subject
		// mutation requires series.Service.Get + episodes.Service.Get
		// + json-deserialise Quality/Languages, none of which the
		// cache itself carries the deps for. Documented in
		// AUDIT_FIX_BACKLOG.md as a follow-up sub-row to C.1.i-follow-5a.
		// Without override, the dispatch uses the original cached
		// Subject which is the common case (user clicked "Download"
		// without manually overriding series/episode/quality).
		_ = dispatcher.ProcessDecisions(r.Context(), []decisionengine.Decision{decision})
		// C# returns the request body back verbatim. Match.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(body)
		_, _ = w.Write(buf)
	})
}

// ReleasePushHandler returns POST /api/v3/release/push.
//
// Mirrors `ReleasePushController.Create` at C#
// `src/Sonarr.Api.V3/Indexers/ReleasePushController.cs:49-82`:
//
//  1. Validate body (Title + DownloadUrl|MagnetUrl + Protocol + PublishDate).
//  2. Build a synthetic ReleaseInfo from the body. Set Guid =
//     `"PUSH-" + DownloadUrl` (C# `:59`). Re-tag with resolved
//     indexer + downloadclient identifiers (DEFERRED — indexer/dc
//     name→id resolution helpers not yet ported; the dispatch still
//     fires correctly using the body's IndexerID + IndexerName).
//  3. Run the synthetic release through `DownloadDecisionMaker.GetRssDecision`
//     with `pushedRelease=true` — matches C# `:69`.
//  4. Process the decision via `ProcessDecisions` (C# `:73`).
//  5. If the decision came back without a parsed episode → 400 with
//     `Title "Unable to parse"` validation failure (C# `:76-79`).
//  6. Return the mapped decision list (C# `:81 — MapDecisions(new[] { decision })`).
//     The body is the same ReleaseResource shape GET /release emits;
//     this initial port returns a stripped-down view of the dispatch
//     outcome (DEFERRED full ReleaseResource projection — see notes).
//
// Audit row C.1.i-follow-5a (J237). Indexer/dc resolve + full
// ReleaseResource response projection deferred.
func ReleasePushHandler(decisionMaker ReleaseDecisionMaker, dispatcher ReleaseDispatcher, cache *releasecache.Cache) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body ReleasePushBody
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("release/push: malformed body: %v", err))
			return
		}
		// PostValidator (C# `:42-47`).
		failures := []httperr.Failure{}
		if body.Title == "" {
			failures = append(failures, httperr.Failure{PropertyName: "Title", ErrorMessage: "'Title' must not be empty.", Severity: "error"})
		}
		if body.DownloadURL == "" && body.MagnetURL == "" {
			failures = append(failures, httperr.Failure{PropertyName: "DownloadUrl", ErrorMessage: "'Download Url' must not be empty.", Severity: "error"})
		}
		if body.Protocol == indexers.DownloadProtocolUnknown {
			failures = append(failures, httperr.Failure{PropertyName: "Protocol", ErrorMessage: "'Protocol' must not be empty.", Severity: "error"})
		}
		if body.PublishDate.IsZero() {
			failures = append(failures, httperr.Failure{PropertyName: "PublishDate", ErrorMessage: "'Publish Date' must not be empty.", Severity: "error"})
		}
		if len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		// Build the synthetic ReleaseInfo. Match C# `ReleaseResource.ToModel`
		// at `ReleaseResource.cs:167-202`.
		var info *parser.ReleaseInfo
		if body.Protocol == indexers.DownloadProtocolTorrent {
			// Torrent variant adds magnet URL. The Go-port's parser.TorrentInfo
			// embeds ReleaseInfo; we marshal as a typed ReleaseInfo with the
			// Torrent fields routed through the existing decision-engine paths.
			t := parser.TorrentInfo{
				ReleaseInfo: parser.ReleaseInfo{
					Title:            body.Title,
					Size:             body.Size,
					DownloadURL:      body.DownloadURL,
					InfoURL:          body.InfoURL,
					CommentURL:       body.CommentURL,
					IndexerID:        body.IndexerID,
					Indexer:          body.Indexer,
					DownloadProtocol: body.Protocol,
					PublishDate:      body.PublishDate.UTC(),
				},
				MagnetURL: body.MagnetURL,
			}
			t.Guid = "PUSH-" + body.DownloadURL
			info = &t.ReleaseInfo
		} else {
			info = &parser.ReleaseInfo{
				Title:            body.Title,
				Size:             body.Size,
				DownloadURL:      body.DownloadURL,
				InfoURL:          body.InfoURL,
				CommentURL:       body.CommentURL,
				IndexerID:        body.IndexerID,
				Indexer:          body.Indexer,
				DownloadProtocol: body.Protocol,
				PublishDate:      body.PublishDate.UTC(),
				Guid:             "PUSH-" + body.DownloadURL,
			}
		}
		// Run decision (single-element batch). pushedRelease=true matches
		// C# `:69` second arg.
		decisions := decisionMaker.GetRssDecision(r.Context(), []any{info}, true)
		if len(decisions) == 0 {
			httperr.WriteValidation(w, []httperr.Failure{{
				PropertyName: "Title",
				ErrorMessage: "Unable to parse",
				Severity:     "error",
			}})
			return
		}
		decision := decisions[0]
		// Verify ParsedEpisodeInfo populated (C# `:76-79` check).
		if sub, ok := decision.Subject.(*decisionengine.Subject); !ok || sub == nil || sub.ParsedEpisodeInfo == nil {
			httperr.WriteValidation(w, []httperr.Failure{{
				PropertyName: "Title",
				ErrorMessage: "Unable to parse",
				Severity:     "error",
			}})
			return
		}
		// Stash in the cache so a subsequent POST /release with the
		// same indexerId+guid (e.g. user-click confirmation) can find
		// it without re-running the decision engine. Mirrors the
		// behaviour of GET /release populating the cache; push
		// effectively replays a single-release decision into the same
		// observable.
		if cache != nil {
			cache.Put(releasecache.Key(body.IndexerID, "PUSH-"+body.DownloadURL), decision)
		}
		// Dispatch.
		_ = dispatcher.ProcessDecisions(r.Context(), []decisionengine.Decision{decision})
		// C# MapDecisions returns a list of ReleaseResource. Full
		// projection is non-trivial (50+ fields, custom-format scoring,
		// scene mapping, language inference). For this initial port we
		// emit a SINGLE-ENTRY list containing only the request body
		// fields + Approved/Rejected flags from the dispatched decision.
		// Documented as DEFERRED — the full projection lands when the
		// existing toReleaseResource helper at release.go:163 gains
		// the missing custom-format / scene-mapping fields.
		resp := []map[string]any{{
			"guid":        "PUSH-" + body.DownloadURL,
			"title":       body.Title,
			"indexer":     body.Indexer,
			"indexerId":   body.IndexerID,
			"protocol":    body.Protocol.String(),
			"publishDate": body.PublishDate.UTC(),
			"downloadUrl": body.DownloadURL,
			"magnetUrl":   body.MagnetURL,
			"size":        body.Size,
			"approved":    decision.Approved(),
			"rejected":    !decision.Approved(),
			"rejections":  rejectionStrings(decision),
		}}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(resp)
		_, _ = w.Write(buf)
	})
}

// rejectionStrings flattens the Decision's rejection list to a string
// slice. Empty list emits `[]` not `null`.
func rejectionStrings(d decisionengine.Decision) []string {
	if len(d.Rejections) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(d.Rejections))
	for _, r := range d.Rejections {
		out = append(out, r.Message)
	}
	return out
}

// ErrReleaseCacheMiss is a sentinel for tests / callers that want to
// distinguish cache-miss from other errors. The 404 path uses
// httperr.NotFound which produces the envelope shape; tests assert on
// status, not on this sentinel.
var ErrReleaseCacheMiss = errors.New("release: cache miss")
