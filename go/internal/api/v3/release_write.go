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
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/releasecache"
	"sonarr.tv/sonarr/internal/series"
)

// ReleaseDispatcher is the narrow dispatch interface for both write
// handlers. Identical shape to QueueGrabDispatcher (J236) — separate
// type alias so the two handler families can be wired independently
// without one's interface changing forcing the other to update.
type ReleaseDispatcher interface {
	ProcessDecisions(ctx context.Context, decisions []decisionengine.Decision) *download.ProcessedDecisions
}

// ReleaseOverrideSeriesLookup is the narrow series-by-id lookup the
// ShouldOverride=true branch uses to swap Subject.Series. Satisfied
// by *series.Service via Go structural typing — no host_wiring.go
// adapter needed. Nil-tolerant from the handler's perspective: when
// nil AND the request has ShouldOverride=true the handler returns
// a 4xx (the operator's UI sent an override but the server isn't
// wired to honour it).
//
// J249.
type ReleaseOverrideSeriesLookup interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// ReleaseOverrideEpisodeLookup is the narrow episodes-by-ids lookup
// the ShouldOverride=true branch uses to swap Subject.Episodes.
// Satisfied by *episodes.Service via Go structural typing.
// Nil-tolerant: same posture as ReleaseOverrideSeriesLookup above.
//
// J249.
type ReleaseOverrideEpisodeLookup interface {
	GetEpisodes(ctx context.Context, ids []int) ([]episodes.Episode, error)
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
func ReleaseDownloadHandler(
	cache *releasecache.Cache,
	dispatcher ReleaseDispatcher,
	seriesLookup ReleaseOverrideSeriesLookup,
	episodeLookup ReleaseOverrideEpisodeLookup,
) http.Handler {
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

		// J249 — ShouldOverride=true arm: clone the cached Subject
		// + override Series / Episodes / Quality / Languages from
		// the request body. Mirrors C# `ReleaseController.cs:84-110`.
		if body.ShouldOverride {
			overridden, status, msg, vfails := applyShouldOverride(r.Context(), &body, decision, seriesLookup, episodeLookup)
			if len(vfails) > 0 {
				httperr.WriteValidation(w, vfails)
				return
			}
			if status != 0 {
				httperr.Write(w, status, msg, "", nil)
				return
			}
			decision = overridden
		}

		_ = dispatcher.ProcessDecisions(r.Context(), []decisionengine.Decision{decision})
		// C# returns the request body back verbatim. Match.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(body)
		_, _ = w.Write(buf)
	})
}

// applyShouldOverride builds the override-mutated Decision when the
// caller set body.ShouldOverride=true. Returns:
//
//   - overridden (Decision, nil-zero-value-on-error): the cached
//     Decision with a shallow-cloned Subject whose Series / Episodes /
//     ParsedEpisodeInfo.Quality / Languages have been swapped per the
//     body. The Release / SceneMapping / MappedSeasonNumber / other
//     decision-engine-computed fields are preserved verbatim from the
//     cache (matches C# `:93-104` — the C# clone enumerates exactly
//     these fields).
//   - status, msg: when non-zero, the handler should write that as the
//     HTTP response. Used for the deps-not-wired (503) and lookup-
//     error (500) cases.
//   - vfails: per-field validation failures when the override body
//     is incomplete (C# `:86-90 Ensure.That` chain).
//
// Mirrors C# `ReleaseController.cs:84-110`. The fields preserved
// verbatim from the cached Subject are listed inline in the C# clone
// expression at `:94-104`; the Go port's `decisionengine.Subject`
// covers the same shape with `Release / SceneMapping /
// MappedSeasonNumber / EpisodeRequested / DownloadAllowed /
// SeedConfiguration / CustomFormats / CustomFormatScore /
// SeriesMatchType / ReleaseSource` plus the overridden Series /
// Episodes / ParsedEpisodeInfo.Quality / Languages.
//
// J249.
func applyShouldOverride(
	ctx context.Context,
	body *ReleaseDownloadBody,
	cached decisionengine.Decision,
	seriesLookup ReleaseOverrideSeriesLookup,
	episodeLookup ReleaseOverrideEpisodeLookup,
) (decisionengine.Decision, int, string, []httperr.Failure) {
	// Validation matching C# `:86-90` Ensure.That chain.
	var vfails []httperr.Failure
	if body.SeriesID <= 0 {
		vfails = append(vfails, httperr.Failure{PropertyName: "SeriesId", ErrorMessage: "'Series Id' must not be empty.", Severity: "error"})
	}
	if len(body.EpisodeIDs) == 0 {
		vfails = append(vfails, httperr.Failure{PropertyName: "EpisodeIds", ErrorMessage: "'Episode Ids' must not be empty.", Severity: "error"})
	}
	if len(body.Quality) == 0 || string(body.Quality) == "null" {
		vfails = append(vfails, httperr.Failure{PropertyName: "Quality", ErrorMessage: "'Quality' must not be empty.", Severity: "error"})
	}
	if len(body.Languages) == 0 || string(body.Languages) == "null" {
		vfails = append(vfails, httperr.Failure{PropertyName: "Languages", ErrorMessage: "'Languages' must not be empty.", Severity: "error"})
	}
	if len(vfails) > 0 {
		return decisionengine.Decision{}, 0, "", vfails
	}

	if seriesLookup == nil || episodeLookup == nil {
		return decisionengine.Decision{}, http.StatusServiceUnavailable, "release override: series/episodes lookup not wired", nil
	}

	srs, err := seriesLookup.GetSeries(ctx, body.SeriesID)
	if err != nil {
		return decisionengine.Decision{}, http.StatusInternalServerError, fmt.Sprintf("release override: series lookup: %v", err), nil
	}
	eps, err := episodeLookup.GetEpisodes(ctx, body.EpisodeIDs)
	if err != nil {
		return decisionengine.Decision{}, http.StatusInternalServerError, fmt.Sprintf("release override: episodes lookup: %v", err), nil
	}

	// Decode quality + languages from body raw JSON.
	var q quality.Model
	if err := json.Unmarshal(body.Quality, &q); err != nil {
		return decisionengine.Decision{}, http.StatusBadRequest, fmt.Sprintf("release override: quality decode: %v", err), nil
	}
	var langs []language.Language
	if err := json.Unmarshal(body.Languages, &langs); err != nil {
		return decisionengine.Decision{}, http.StatusBadRequest, fmt.Sprintf("release override: languages decode: %v", err), nil
	}

	// Shallow-clone the Subject (matches C# `:94-104` "new RemoteEpisode { ... }").
	// The cached Subject is a `*decisionengine.Subject`; type-assert + dereference
	// to copy the underlying struct.
	origSubj, ok := cached.Subject.(*decisionengine.Subject)
	if !ok || origSubj == nil {
		return decisionengine.Decision{}, http.StatusInternalServerError, "release override: cached subject shape unexpected", nil
	}
	cloned := *origSubj
	// ParsedEpisodeInfo is referenced by pointer in Subject; the C#
	// `JsonClone()` call at `:96` deep-clones it before we mutate
	// .Quality. We need an equivalent shallow-of-the-struct clone so
	// the original cached ParsedEpisodeInfo isn't mutated.
	if origSubj.ParsedEpisodeInfo != nil {
		parsedClone := *origSubj.ParsedEpisodeInfo
		parsedClone.Quality = q
		cloned.ParsedEpisodeInfo = &parsedClone
	}
	cloned.Series = srs
	cloned.Episodes = eps
	cloned.Languages = langs

	// Build the overridden Decision. Decision struct itself is
	// value-typed; copy + swap Subject.
	out := cached
	out.Subject = &cloned
	return out, 0, "", nil
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
