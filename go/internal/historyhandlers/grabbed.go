// Package historyhandlers holds the IHandle<TEvent> port equivalents
// for [history.Service]'s seven event subscribers. Lives in its own
// package because [decisionengine] already imports [history] (for
// AlreadyImportedSpec's history.Entry / EventType types) — putting
// the handlers ON [history.Service] directly would create an import
// cycle history → decisionengine → history. The handlers here import
// both [history] (Entry / Service.Add) and [decisionengine] (the
// EpisodeGrabbedEvent payload + Subject) freely.
//
// Each handler builds [history.Entry] rows from an event payload and
// routes them through [*history.Service.Add]. The cmd/sonarr boot
// site subscribes one lambda per handler at the message bus.
//
// # Where this lives in the C# tree
//
// The C# `HistoryService` ports all seven IHandle implementations
// directly on the service class itself (`HistoryService.cs:144-373`).
// The Go-port factors them into this dedicated package per the cycle
// note above. The handler bodies are still 1:1 with their C#
// counterparts — the placement difference is the only divergence.
package historyhandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// EntryWriter narrows [*history.Service] to the one method this
// package needs (Add). Pulled out so tests can inject a stub
// without constructing the full service.
type EntryWriter interface {
	Add(ctx context.Context, e *history.Entry) (*history.Entry, error)
}

// HandleEpisodeGrabbed ports
// `src/NzbDrone.Core/History/HistoryService.cs:144-194` —
// `Handle(EpisodeGrabbedEvent)`. Walks every episode in the grabbed
// Subject and writes one [history.Entry] per episode through
// [EntryWriter.Add], with the release metadata captured into the
// Entry's Data map.
//
// # Per-episode Entry shape (mirrors C# `:148-158`)
//
//   - EventType = [history.EventTypeGrabbed]
//   - Date = time.Now().UTC() (call-time clock)
//   - Quality = JSON-encoded Subject.ParsedEpisodeInfo.Quality
//   - SourceTitle = Subject.Release.Title (typed via [parser.AsTorrentInfo]
//     fallback → embedded ReleaseInfo)
//   - SeriesID / EpisodeID = the iterated episode's row ids
//   - DownloadID = ev.DownloadID (verbatim — empty if no download
//     client involvement, e.g. force-import flow)
//   - Languages = Subject.Languages flattened to []int IDs
//
// # Data map (mirrors C# `:160-185`)
//
// 19 fixed entries — Indexer / NzbInfoUrl / ReleaseGroup / Age /
// AgeHours / AgeMinutes / PublishedDate / DownloadClient /
// DownloadClientName / Size / DownloadUrl / Guid / TvdbId / TvRageId /
// ImdbId / Protocol / CustomFormatScore / SeriesMatchType /
// ReleaseSource / IndexerFlags / ReleaseType. ReleaseHash is appended
// only when non-empty (matches C# `:182-185`). TorrentInfoHash is
// appended only when the release is a [parser.TorrentInfo] (matches
// C# `:187-190`).
//
// # ReleaseType handling
//
// The C# event reads `message.Episode.ParsedEpisodeInfo.ReleaseType`.
// Go's [parser.ParsedEpisodeInfo] exposes the same value as a derived
// method `ReleaseType()` (not a field — same semantic). The handler
// sources from `subject.ParsedEpisodeInfo.ReleaseType()` and emits
// the PascalCase enum name via [parser.ReleaseType.String], matching
// C# `HistoryService.cs:180` (`message.Episode.ParsedEpisodeInfo.ReleaseType.ToString()`).
// Nil-ParsedEpisodeInfo degrades to [parser.ReleaseTypeUnknown]
// (`"Unknown"`). Slice 5.J159 sweep flipped this from the int-string
// form to the PascalCase name across all 5 history handlers
// (grabbed / imported / file_deleted / file_renamed /
// download_ignored) for byte-equal contract with the C# wire shape;
// [parser.ParseReleaseType] still accepts both the numeric and named
// forms case-insensitively so any consumer round-tripping through
// it stays compatible.
//
// # Defensive arms
//
// nil-writer / nil-event / nil-event.Episode → no-op (no Entry
// written, no error). Empty Subject.Episodes → no-op. nil-Release /
// nil-PEI produce empty SourceTitle and zero-valued Quality — the
// row is still written so the audit trail captures even malformed
// grab payloads (matches the C# tree's posture; the C# Insert call
// is inside the foreach with no guards). Per-episode Add errors
// abort the loop and propagate (the C# tree relies on EF transaction
// semantics; the Go port surfaces the first per-row failure rather
// than swallowing).
func HandleEpisodeGrabbed(ctx context.Context, w EntryWriter, ev *decisionengine.EpisodeGrabbedEvent) error {
	if w == nil {
		return errors.New("historyhandlers: HandleEpisodeGrabbed: nil writer")
	}
	if ev == nil || ev.Episode == nil {
		return nil
	}
	subject := ev.Episode
	if len(subject.Episodes) == 0 {
		return nil
	}

	rel := releaseInfoFromSubject(subject)
	torrent := parser.AsTorrentInfo(subject.Release)

	sourceTitle := ""
	if rel != nil {
		sourceTitle = rel.Title
	}

	quality, err := encodeQuality(subject)
	if err != nil {
		return fmt.Errorf("historyhandlers: HandleEpisodeGrabbed: encode quality: %w", err)
	}

	languages := languageIDs(subject)

	now := time.Now().UTC()
	for _, ep := range subject.Episodes {
		data := buildGrabbedData(ev, subject, rel, torrent)
		entry := &history.Entry{
			EventType:   eventTypePtr(history.EventTypeGrabbed),
			Date:        now,
			Quality:     quality,
			SourceTitle: sourceTitle,
			SeriesID:    ep.SeriesID,
			EpisodeID:   ep.ID,
			DownloadID:  ev.DownloadID,
			Languages:   languages,
			Data:        data,
		}
		if _, err := w.Add(ctx, entry); err != nil {
			return fmt.Errorf("historyhandlers: HandleEpisodeGrabbed(episode=%d): %w", ep.ID, err)
		}
	}
	return nil
}

// releaseInfoFromSubject narrows Subject.Release (any) to a
// [*parser.ReleaseInfo] regardless of whether the underlying release
// is a plain Newznab [parser.ReleaseInfo] or a torrent-extended
// [parser.TorrentInfo] (which embeds ReleaseInfo). Returns nil when
// the release is neither (constructor bug or test fixture without
// a release).
func releaseInfoFromSubject(s *decisionengine.Subject) *parser.ReleaseInfo {
	if s == nil {
		return nil
	}
	if r, ok := s.Release.(*parser.ReleaseInfo); ok && r != nil {
		return r
	}
	if t, ok := s.Release.(*parser.TorrentInfo); ok && t != nil {
		return &t.ReleaseInfo
	}
	return nil
}

// encodeQuality JSON-marshals Subject.ParsedEpisodeInfo.Quality into
// the persisted-row form. nil-PEI yields the empty-object form `{}`
// matching the C# null-quality fallback.
func encodeQuality(s *decisionengine.Subject) (string, error) {
	if s == nil || s.ParsedEpisodeInfo == nil {
		return "{}", nil
	}
	b, err := json.Marshal(s.ParsedEpisodeInfo.Quality)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// languageIDs flattens Subject.Languages to the persisted []int form
// (the History.Languages column is a JSON array of language IDs).
func languageIDs(s *decisionengine.Subject) []int {
	if s == nil || len(s.Languages) == 0 {
		return []int{}
	}
	out := make([]int, 0, len(s.Languages))
	for _, l := range s.Languages {
		out = append(out, l.ID)
	}
	return out
}

// buildGrabbedData materialises the C# `:160-190` Data dictionary
// build sequence. All values converted to strings via the same
// formatters the C# code uses (.ToString() on int / float / enum;
// `'s'+'Z'` for the PublishedDate's UTC ISO format).
func buildGrabbedData(ev *decisionengine.EpisodeGrabbedEvent, subject *decisionengine.Subject, rel *parser.ReleaseInfo, torrent *parser.TorrentInfo) map[string]string {
	data := make(map[string]string, 22)
	if rel != nil {
		data["Indexer"] = rel.Indexer
		data["NzbInfoUrl"] = rel.InfoURL
		data["Age"] = strconv.Itoa(rel.Age())
		data["AgeHours"] = formatFloat(rel.AgeHours())
		data["AgeMinutes"] = formatFloat(rel.AgeMinutes())
		data["PublishedDate"] = rel.PublishDate.UTC().Format("2006-01-02T15:04:05") + "Z"
		data["Size"] = strconv.FormatInt(rel.Size, 10)
		data["DownloadUrl"] = rel.DownloadURL
		data["Guid"] = rel.Guid
		data["TvdbId"] = strconv.Itoa(rel.TvdbID)
		data["TvRageId"] = strconv.Itoa(rel.TvRageID)
		data["ImdbId"] = rel.ImdbID
		data["Protocol"] = strconv.Itoa(int(rel.DownloadProtocol))
		// IndexerFlags emits the comma-separated `[Flags]` name form
		// via [parser.IndexerFlags.String] — J160 sweep — matching C#
		// `HistoryService.cs:179`
		// (`message.Episode.Release.IndexerFlags.ToString()`). The
		// audit-log column on disk stays the int form;
		// [parser.ParseIndexerFlags] accepts both shapes.
		data["IndexerFlags"] = rel.IndexerFlags.String()
	} else {
		data["Indexer"] = ""
		data["NzbInfoUrl"] = ""
		data["Age"] = "0"
		data["AgeHours"] = "0"
		data["AgeMinutes"] = "0"
		data["PublishedDate"] = ""
		data["Size"] = "0"
		data["DownloadUrl"] = ""
		data["Guid"] = ""
		data["TvdbId"] = "0"
		data["TvRageId"] = "0"
		data["ImdbId"] = ""
		data["Protocol"] = "0"
		data["IndexerFlags"] = "0"
	}

	if subject != nil && subject.ParsedEpisodeInfo != nil {
		data["ReleaseGroup"] = subject.ParsedEpisodeInfo.ReleaseGroup
		if subject.ParsedEpisodeInfo.ReleaseHash != "" {
			data["ReleaseHash"] = subject.ParsedEpisodeInfo.ReleaseHash
		}
	} else {
		data["ReleaseGroup"] = ""
	}

	data["DownloadClient"] = ev.DownloadClient
	data["DownloadClientName"] = ev.DownloadClientName

	if subject != nil {
		data["CustomFormatScore"] = strconv.Itoa(subject.CustomFormatScore)
		data["SeriesMatchType"] = matchTypeName(subject.SeriesMatchType)
		data["ReleaseSource"] = releaseSourceName(subject.ReleaseSource)
	} else {
		data["CustomFormatScore"] = "0"
		data["SeriesMatchType"] = matchTypeName(series.MatchTypeUnknown)
		data["ReleaseSource"] = releaseSourceName(decisionengine.ReleaseSourceUnknown)
	}
	// Source ReleaseType from subject.ParsedEpisodeInfo.ReleaseType()
	// (a derived method on the parsed-info value), emitted as the
	// int-string form to match the sister handlers. Nil-PEI → "0"
	// (ReleaseTypeUnknown).
	rt := parser.ReleaseTypeUnknown
	if subject != nil && subject.ParsedEpisodeInfo != nil {
		rt = subject.ParsedEpisodeInfo.ReleaseType()
	}
	data["ReleaseType"] = rt.String()

	if torrent != nil {
		data["TorrentInfoHash"] = torrent.InfoHash
	}
	return data
}

// formatFloat mirrors C# `double.ToString()` default — uses the
// general format with no fixed precision. Go's `strconv.FormatFloat`
// with `-1` precision produces the shortest round-trippable form,
// which differs from C#'s default in edge cases but is byte-identical
// for values typical of AgeHours / AgeMinutes (small positive doubles).
func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// eventTypePtr returns &v as a *int. The History.EventType column is
// nullable in the schema; the Service emits a non-nil pointer for
// every Entry the handler writes.
func eventTypePtr(t history.EventType) *int {
	v := int(t)
	return &v
}

// matchTypeName mirrors C# `series.MatchType.ToString()` — the Go
// MatchType enum doesn't ship a String method (the type definition
// has the names as comments only), so emit them here for log /
// audit-trail use.
func matchTypeName(t series.MatchType) string {
	switch t {
	case series.MatchTypeUnknown:
		return "Unknown"
	case series.MatchTypeTitle:
		return "Title"
	case series.MatchTypeAlias:
		return "Alias"
	case series.MatchTypeID:
		return "Id"
	default:
		return "Unknown"
	}
}

// releaseSourceName mirrors C#
// `decisionengine.ReleaseSourceType.ToString()`. Same shape as
// matchTypeName — the Go enum doesn't ship a String method.
func releaseSourceName(t decisionengine.ReleaseSourceType) string {
	switch t {
	case decisionengine.ReleaseSourceUnknown:
		return "Unknown"
	case decisionengine.ReleaseSourceRSS:
		return "Rss"
	case decisionengine.ReleaseSourceSearch:
		return "Search"
	case decisionengine.ReleaseSourceUserInvokedSearch:
		return "UserInvokedSearch"
	case decisionengine.ReleaseSourceInteractiveSearch:
		return "InteractiveSearch"
	case decisionengine.ReleaseSourceReleasePush:
		return "ReleasePush"
	default:
		return "Unknown"
	}
}
