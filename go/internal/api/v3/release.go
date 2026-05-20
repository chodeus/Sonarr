package v3

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/parser"
)

// ReleaseResource mirrors `src/Sonarr.Api.V3/Indexers/ReleaseResource.cs`
// — the per-release wire shape returned by `GET /api/v3/release` /
// manual-search results.
//
// **Minimal projection**: the full C# ReleaseResource has ~50 fields;
// this initial port covers the core read-side subset the React UI
// surfaces in the manual-search dialog (Title / Quality / Indexer /
// Size / Age / Languages / Approved+Rejections / SeasonNumber /
// EpisodeNumbers / FullSeason). Custom-format scoring, scene-mapping
// indicators, magnet-url + torrent peers, and the "send when queuing
// unknown release" fields land in a follow-up slice — they're either
// produced from subsystems still being wired (custom-format calc on
// search) or are write-side echoes the GET path doesn't need.
//
// Audit fix B.4 (initial wiring).
type ReleaseResource struct {
	Guid                   string          `json:"guid"`
	Quality                json.RawMessage `json:"quality"`
	Age                    int             `json:"age"`
	AgeHours               float64         `json:"ageHours"`
	AgeMinutes             float64         `json:"ageMinutes"`
	Size                   int64           `json:"size"`
	IndexerID              int             `json:"indexerId"`
	Indexer                string          `json:"indexer"`
	ReleaseGroup           string          `json:"releaseGroup"`
	ReleaseHash            string          `json:"releaseHash,omitempty"`
	Title                  string          `json:"title"`
	FullSeason             bool            `json:"fullSeason"`
	SeasonNumber           int             `json:"seasonNumber"`
	Languages              []int           `json:"languages"`
	AirDate                string          `json:"airDate,omitempty"`
	SeriesTitle            string          `json:"seriesTitle,omitempty"`
	EpisodeNumbers         []int           `json:"episodeNumbers"`
	AbsoluteEpisodeNumbers []int           `json:"absoluteEpisodeNumbers"`
	Approved               bool            `json:"approved"`
	TemporarilyRejected    bool            `json:"temporarilyRejected"`
	Rejected               bool            `json:"rejected"`
	TvdbID                 int             `json:"tvdbId"`
	TvRageID               int             `json:"tvRageId"`
	ImdbID                 string          `json:"imdbId,omitempty"`
	Rejections             []string        `json:"rejections"`
	PublishDate            string          `json:"publishDate"`
	CommentURL             string          `json:"commentUrl,omitempty"`
	DownloadURL            string          `json:"downloadUrl,omitempty"`
	InfoURL                string          `json:"infoUrl,omitempty"`
	Protocol               string          `json:"protocol"`
	IndexerFlags           int             `json:"indexerFlags"`
	IsDaily                bool            `json:"isDaily"`
	IsAbsoluteNumbering    bool            `json:"isAbsoluteNumbering"`
	IsPossibleSpecial      bool            `json:"isPossibleSpecialEpisode"`
	Special                bool            `json:"special"`
	MagnetURL              string          `json:"magnetUrl,omitempty"`
	InfoHash               string          `json:"infoHash,omitempty"`
	Seeders                *int            `json:"seeders,omitempty"`
	Leechers               *int            `json:"leechers,omitempty"`
	MappedSeasonNumber     *int            `json:"mappedSeasonNumber,omitempty"`
	MappedEpisodeNumbers   []int           `json:"mappedEpisodeNumbers,omitempty"`
}

// ReleaseSearcher is the narrow projection the handler uses for
// indexer search. *indexersearch.Service satisfies it directly via
// the EpisodeSearch / SeasonSearch methods.
type ReleaseSearcher interface {
	EpisodeSearch(ctx context.Context, episodeID int, settings indexersearch.SearchSettings) ([]decisionengine.Decision, error)
	SeasonSearch(ctx context.Context, seriesID, seasonNumber int, missingOnly bool, settings indexersearch.SearchSettings) ([]decisionengine.Decision, error)
}

// ReleaseHandler returns GET /api/v3/release. Mirrors C#
// `ReleaseController.GetReleases` at `ReleaseController.cs:171-186`
// which dispatches on episodeId / (seriesId + seasonNumber) / no-args
// (RSS-feed) branches.
//
// Dispatch:
//   - episodeId set → EpisodeSearch.
//   - seriesId + seasonNumber set → SeasonSearch (missingOnly=false to
//     match the C# `GetSeasonReleases` call site at `:212`).
//   - neither → empty array (RSS-fetch search isn't ported yet; the
//     C# `GetRss` branch calls `_releaseSearchService.GetRss()`).
//
// Results are projected to the minimal ReleaseResource shape; the
// full ~50-field C# wire shape lands in a follow-up.
//
// Audit fix B.4.
func ReleaseHandler(searcher ReleaseSearcher) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seriesID, ok := parseOptionalInt(w, q.Get("seriesId"), "seriesId")
		if !ok {
			return
		}
		episodeID, ok := parseOptionalInt(w, q.Get("episodeId"), "episodeId")
		if !ok {
			return
		}
		seasonNumber, ok := parseOptionalInt(w, q.Get("seasonNumber"), "seasonNumber")
		if !ok {
			return
		}

		var decisions []decisionengine.Decision
		var err error
		ctx := r.Context()
		settings := indexersearch.SearchSettings{}

		switch {
		case episodeID != 0 && searcher != nil:
			decisions, err = searcher.EpisodeSearch(ctx, episodeID, settings)
		case seriesID != 0 && seasonNumber != 0 && searcher != nil:
			decisions, err = searcher.SeasonSearch(ctx, seriesID, seasonNumber, false, settings)
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, err, nil)
			return
		}

		out := make([]ReleaseResource, 0, len(decisions))
		for _, d := range decisions {
			out = append(out, toReleaseResource(d))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// parseOptionalInt validates an optional int-typed query parameter.
// Returns 0 when the param is empty; the bool is true on success
// (continue handling) and false when a 400 has already been written.
func parseOptionalInt(w http.ResponseWriter, raw, name string) (int, bool) {
	if raw == "" {
		return 0, true
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		httperr.BadRequest(w, "release: invalid "+name)
		return 0, false
	}
	return v, true
}

// toReleaseResource projects a decisionengine.Decision into the
// minimal ReleaseResource shape. Pulls fields off the
// *decisionengine.Subject (Subject is `any` because the package is
// kept agnostic of the concrete RemoteEpisode type) — we type-assert
// to *decisionengine.Subject; unknown subject types produce a
// best-effort empty resource (matches C# behaviour when
// `model.RemoteEpisode` is unset).
func toReleaseResource(d decisionengine.Decision) ReleaseResource {
	subj, ok := d.Subject.(*decisionengine.Subject)
	if !ok || subj == nil {
		return ReleaseResource{
			Approved:   d.Approved(),
			Rejected:   d.Rejected(),
			Rejections: rejectionMessages(d),
		}
	}
	r := ReleaseResource{
		Approved:            d.Approved(),
		TemporarilyRejected: d.TemporarilyRejected(),
		Rejected:            d.Rejected(),
		Rejections:          rejectionMessages(d),
	}
	if rel := asReleaseInfo(subj.Release); rel != nil {
		r.Guid = rel.Guid
		r.Title = rel.Title
		r.Indexer = rel.Indexer
		r.IndexerID = rel.IndexerID
		r.Size = rel.Size
		r.DownloadURL = rel.DownloadURL
		r.InfoURL = rel.InfoURL
		r.CommentURL = rel.CommentURL
		r.TvdbID = rel.TvdbID
		r.TvRageID = rel.TvRageID
		r.ImdbID = rel.ImdbID
		r.PublishDate = rel.PublishDate.UTC().Format("2006-01-02T15:04:05Z")
		r.Protocol = rel.DownloadProtocol.String()
		r.IndexerFlags = int(rel.IndexerFlags)
		r.Age = rel.Age()
		r.AgeHours = rel.AgeHours()
		r.AgeMinutes = rel.AgeMinutes()
	}
	if torrent := parser.AsTorrentInfo(subj.Release); torrent != nil {
		r.MagnetURL = torrent.MagnetURL
		r.InfoHash = torrent.InfoHash
		if torrent.Seeders != nil {
			v := *torrent.Seeders
			r.Seeders = &v
		}
		if torrent.Peers != nil && torrent.Seeders != nil {
			leechers := *torrent.Peers - *torrent.Seeders
			r.Leechers = &leechers
		}
	}
	if pei := subj.ParsedEpisodeInfo; pei != nil {
		r.ReleaseGroup = pei.ReleaseGroup
		r.ReleaseHash = pei.ReleaseHash
		r.FullSeason = pei.FullSeason
		r.SeasonNumber = pei.SeasonNumber
		r.AirDate = pei.AirDate
		r.SeriesTitle = pei.SeriesTitle
		r.EpisodeNumbers = append([]int(nil), pei.EpisodeNumbers...)
		r.AbsoluteEpisodeNumbers = append([]int(nil), pei.AbsoluteEpisodeNumbers...)
		r.IsDaily = pei.IsDaily()
		r.IsAbsoluteNumbering = pei.IsAbsoluteNumbering()
		r.IsPossibleSpecial = pei.IsPossibleSpecialEpisode()
		r.Special = pei.Special
		if q, err := json.Marshal(pei.Quality); err == nil {
			r.Quality = q
		}
	}
	// Languages: project to []int IDs only; full Language objects
	// are deferred (full ReleaseResource has a richer Language
	// struct).
	r.Languages = make([]int, 0, len(subj.Languages))
	for _, l := range subj.Languages {
		r.Languages = append(r.Languages, l.ID)
	}
	// Mapped episodes (when set, the search has resolved the release
	// to specific episodes — the C# `model.RemoteEpisode.Episodes`).
	if len(subj.Episodes) > 0 {
		s := subj.Episodes[0].SeasonNumber
		r.MappedSeasonNumber = &s
		r.MappedEpisodeNumbers = make([]int, 0, len(subj.Episodes))
		for _, ep := range subj.Episodes {
			r.MappedEpisodeNumbers = append(r.MappedEpisodeNumbers, ep.EpisodeNumber)
		}
	}
	// Always emit empty arrays (not null) for the slice wire-shape
	// contract — C# defaults are List<int>() initialised empty.
	if r.EpisodeNumbers == nil {
		r.EpisodeNumbers = []int{}
	}
	if r.AbsoluteEpisodeNumbers == nil {
		r.AbsoluteEpisodeNumbers = []int{}
	}
	if r.Languages == nil {
		r.Languages = []int{}
	}
	if r.Rejections == nil {
		r.Rejections = []string{}
	}
	return r
}

// asReleaseInfo wraps the type assertion the per-subject mapper uses
// to pull a *parser.ReleaseInfo out of Subject.Release. Mirrors the
// AsTorrentInfo idiom in `internal/parser/release_info.go`; lives
// here so internal/api/v3 doesn't need to import parser purely for a
// type assertion the parser package doesn't currently export.
func asReleaseInfo(release any) *parser.ReleaseInfo {
	if r, ok := release.(*parser.ReleaseInfo); ok {
		return r
	}
	if t, ok := release.(*parser.TorrentInfo); ok {
		return &t.ReleaseInfo
	}
	return nil
}

// rejectionMessages projects the Rejection slice down to the C#
// `Rejections.Select(r => r.Message)` form the JSON wire shape uses.
func rejectionMessages(d decisionengine.Decision) []string {
	if len(d.Rejections) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(d.Rejections))
	for _, rj := range d.Rejections {
		out = append(out, rj.Message)
	}
	return out
}
