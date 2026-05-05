package parser

import (
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/pendingreleases"
)

// ReleaseInfo mirrors the C# `NzbDrone.Core.Parser.Model.ReleaseInfo`
// at `src/NzbDrone.Core/Parser/Model/ReleaseInfo.cs:1-114`. The shared
// projection of one release (Newznab, Torznab, manual push) before
// it's bound to a parsed series + episodes by the higher-level
// IndexerSearch / DownloadDecisionMaker pipelines.
//
// Wire shape: serialised on the v3 `release` resources and inside
// every History row's `data` blob. Field names + JSON tags MUST match
// the C# `JsonPropertyName` defaults exactly so existing consumers
// (the React frontend, third-party API clients, persisted History
// rows from old C# instances) continue to round-trip.
//
// # The TorrentInfo gotcha
//
// The C# tree uses inheritance: `TorrentInfo : ReleaseInfo`. Indexers
// return concrete `TorrentInfo` instances for Torznab; downstream
// reads use the `as TorrentInfo` cast pattern (see TorrentInfo
// `GetSeeders` / `GetPeers` static helpers). Go has no equivalent —
// we model the relationship via composition: `TorrentInfo` embeds
// `ReleaseInfo`, and call sites that need the torrent-specific
// fields type-assert to `*TorrentInfo`.
//
// To preserve the C# `[JsonIgnore]` semantics on `IndexerFlags` and
// `PendingReleaseReason`, both fields carry `json:"-"` tags so they
// don't leak into the v3 wire surface.
type ReleaseInfo struct {
	Guid                                string                    `json:"guid"`
	Title                               string                    `json:"title"`
	Size                                int64                     `json:"size"`
	DownloadURL                         string                    `json:"downloadUrl"`
	InfoURL                             string                    `json:"infoUrl"`
	CommentURL                          string                    `json:"commentUrl"`
	IndexerID                           int                       `json:"indexerId"`
	Indexer                             string                    `json:"indexer"`
	IndexerPriority                     int                       `json:"indexerPriority"`
	SeasonSearchMaximumSingleEpisodeAge int                       `json:"seasonSearchMaximumSingleEpisodeAge"`
	DownloadProtocol                    indexers.DownloadProtocol `json:"downloadProtocol"`
	TvdbID                              int                       `json:"tvdbId"`
	TvRageID                            int                       `json:"tvRageId"`
	ImdbID                              string                    `json:"imdbId"`
	PublishDate                         time.Time                 `json:"publishDate"`
	Origin                              string                    `json:"origin"`
	Source                              string                    `json:"source"`
	Container                           string                    `json:"container"`
	Codec                               string                    `json:"codec"`
	Resolution                          string                    `json:"resolution"`
	Languages                           []language.Language       `json:"languages"`
	IndexerFlags                        IndexerFlags              `json:"-"`
	PendingReleaseReason                *pendingreleases.Reason   `json:"-"`
}

// Age returns whole days since PublishDate (UTC), mirroring the C#
// `Age` derived property at `:49-58`. The C# tree exposes `Age` as a
// public int — read-only when the caller is computing freshness gates
// (MinimumAgeSpecification, RetentionSpecification). The Go port
// derives it the same way: now-publish, in days, truncated.
//
// The clock is read at call time — pin time.Now via test helpers
// that override the now func at the call site if you need
// determinism (no override hook lives on ReleaseInfo because the C#
// tree doesn't have one either; specs that need clock pinning inject
// their own).
func (r *ReleaseInfo) Age() int {
	return int(time.Since(r.PublishDate).Hours() / 24)
}

// AgeHours returns fractional hours since PublishDate. Mirrors C#
// `:61-70`. Used by MinimumAgeSpecification when the threshold is
// expressed in hours rather than days.
func (r *ReleaseInfo) AgeHours() float64 {
	return time.Since(r.PublishDate).Hours()
}

// AgeMinutes returns fractional minutes since PublishDate. Mirrors C#
// `:73-82`. Used by DelayProfile-driven specs where the "minimum age"
// tier is configured in minutes.
func (r *ReleaseInfo) AgeMinutes() float64 {
	return time.Since(r.PublishDate).Minutes()
}

// String mirrors the C# default ToString at `:85-88`:
// `[<publishDate>] <title> [<size>]`. Used in log lines and rejection
// messages.
func (r *ReleaseInfo) String() string {
	return fmt.Sprintf("[%s] %s [%d]", r.PublishDate.Format(time.RFC3339), r.Title, r.Size)
}

// TorrentInfo extends ReleaseInfo with torrent-specific metadata.
// Mirrors the C# `TorrentInfo : ReleaseInfo` at
// `src/NzbDrone.Core/Parser/Model/TorrentInfo.cs:1-52`. Embeds
// ReleaseInfo by value so callers that have a *TorrentInfo can pass
// `&t.ReleaseInfo` to functions taking *ReleaseInfo (the inverse of
// C#'s implicit upcast).
//
// Seeders/Peers are pointers to mirror the C# `int?` nullable shape;
// Newznab indexers + non-torrent sources leave them nil, and downstream
// specs (TorrentSeedingSpecification) gate on nil-ness before reading.
type TorrentInfo struct {
	ReleaseInfo
	MagnetURL string `json:"magnetUrl"`
	InfoHash  string `json:"infoHash"`
	Seeders   *int   `json:"seeders"`
	Peers     *int   `json:"peers"`
}

// AsTorrentInfo is the Go-shape replacement for the C# `release as
// TorrentInfo` cast pattern. Returns the *TorrentInfo if release is
// one, else nil. Callers route as:
//
//	if t := parser.AsTorrentInfo(release); t != nil {
//	    if t.Seeders != nil && *t.Seeders < min { ... }
//	}
//
// Mirrors the static C# helpers `TorrentInfo.GetSeeders` /
// `GetPeers` at `:12-34` but exposes the typed pointer rather than
// pre-dereferencing one field — the typed shape composes into other
// torrent-only access patterns (MagnetURL, InfoHash) without
// per-field helpers.
func AsTorrentInfo(release any) *TorrentInfo {
	if t, ok := release.(*TorrentInfo); ok {
		return t
	}
	return nil
}

// GetSeeders mirrors C# `TorrentInfo.GetSeeders(release)` at `:12-22`.
// Returns nil when release is not a torrent or when the torrent's
// Seeders field is itself nil.
func GetSeeders(release any) *int {
	if t := AsTorrentInfo(release); t != nil {
		return t.Seeders
	}
	return nil
}

// GetPeers mirrors C# `TorrentInfo.GetPeers(release)` at `:24-34`.
func GetPeers(release any) *int {
	if t := AsTorrentInfo(release); t != nil {
		return t.Peers
	}
	return nil
}
