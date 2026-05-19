package historyhandlers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/historyhandlers"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// stubWriter captures every Add call for assertion + can simulate
// repo errors via the err field.
type stubWriter struct {
	added []*history.Entry
	err   error
}

func (s *stubWriter) Add(_ context.Context, e *history.Entry) (*history.Entry, error) {
	if s.err != nil {
		return nil, s.err
	}
	clone := *e
	clone.ID = len(s.added) + 1
	s.added = append(s.added, &clone)
	return &clone, nil
}

func grabbedSubject(episodes_ []episodes.Episode, release any) *decisionengine.Subject {
	return &decisionengine.Subject{
		Episodes:        episodes_,
		Release:         release,
		Series:          &series.Series{ID: 7},
		SeriesMatchType: series.MatchTypeAlias,
		ReleaseSource:   decisionengine.ReleaseSourceInteractiveSearch,
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			Quality:      quality.Model{Quality: quality.Bluray1080p, Revision: quality.Revision{Version: 2}},
			ReleaseGroup: "GROUP",
			ReleaseHash:  "abc123",
		},
		Languages:         []language.Language{language.English, {ID: 26, Name: "Spanish"}},
		CustomFormatScore: 75,
	}
}

func grabbedRelease() *parser.ReleaseInfo {
	return &parser.ReleaseInfo{
		Title:            "Show.S01E01.1080p.BluRay-GROUP",
		Guid:             "guid-1",
		Size:             1_000_000_000,
		DownloadURL:      "https://x/d",
		InfoURL:          "https://x/i",
		Indexer:          "Indexer-X",
		DownloadProtocol: indexers.DownloadProtocolUsenet,
		TvdbID:           99999,
		TvRageID:         12345,
		ImdbID:           "tt0001",
		PublishDate:      time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		IndexerFlags:     parser.IndexerFlagFreeleech | parser.IndexerFlagInternal,
	}
}

func TestHandleEpisodeGrabbed_NilWriter(t *testing.T) {
	t.Parallel()
	if err := historyhandlers.HandleEpisodeGrabbed(t.Context(), nil, &decisionengine.EpisodeGrabbedEvent{}); err == nil {
		t.Errorf("expected error on nil writer")
	}
}

func TestHandleEpisodeGrabbed_NoOpOnNilEvent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := historyhandlers.HandleEpisodeGrabbed(t.Context(), w, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("nil event: writer should not be called; got %d entries", len(w.added))
	}
}

func TestHandleEpisodeGrabbed_NoOpOnNilSubject(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := historyhandlers.HandleEpisodeGrabbed(t.Context(), w, &decisionengine.EpisodeGrabbedEvent{}); err != nil {
		t.Errorf("nil subject: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("nil subject: should be no-op; got %d entries", len(w.added))
	}
}

func TestHandleEpisodeGrabbed_NoOpOnEmptyEpisodes(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := &decisionengine.EpisodeGrabbedEvent{
		Episode: &decisionengine.Subject{},
	}
	if err := historyhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Errorf("empty episodes: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("empty episodes: should be no-op; got %d", len(w.added))
	}
}

func TestHandleEpisodeGrabbed_WritesPerEpisode(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}, {ID: 12, SeriesID: 7}}
	subject := grabbedSubject(eps, grabbedRelease())
	ev := &decisionengine.EpisodeGrabbedEvent{
		Episode:            subject,
		DownloadID:         "download-token",
		DownloadClient:     "Sabnzbd",
		DownloadClientName: "Local Sab",
	}
	if err := historyhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	if len(w.added) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(w.added))
	}
	for i, want := range []struct {
		episodeID int
		seriesID  int
	}{{11, 7}, {12, 7}} {
		got := w.added[i]
		if got.EpisodeID != want.episodeID {
			t.Errorf("entry %d EpisodeID = %d, want %d", i, got.EpisodeID, want.episodeID)
		}
		if got.SeriesID != want.seriesID {
			t.Errorf("entry %d SeriesID = %d, want %d", i, got.SeriesID, want.seriesID)
		}
		if got.SourceTitle != "Show.S01E01.1080p.BluRay-GROUP" {
			t.Errorf("entry %d SourceTitle = %q", i, got.SourceTitle)
		}
		if got.DownloadID != "download-token" {
			t.Errorf("entry %d DownloadID = %q", i, got.DownloadID)
		}
		if got.EventType == nil || history.EventType(*got.EventType) != history.EventTypeGrabbed {
			t.Errorf("entry %d EventType = %v, want Grabbed", i, got.EventType)
		}
		if got.Date.IsZero() {
			t.Errorf("entry %d Date is zero", i)
		}
	}
}

func TestHandleEpisodeGrabbed_DataMapShape(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	subject := grabbedSubject(eps, grabbedRelease())
	ev := &decisionengine.EpisodeGrabbedEvent{
		Episode:            subject,
		DownloadID:         "tok-1",
		DownloadClient:     "QBittorrent",
		DownloadClientName: "Local QB",
	}
	if err := historyhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	if len(w.added) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(w.added))
	}
	d := w.added[0].Data
	cases := []struct{ key, want string }{
		{"Indexer", "Indexer-X"},
		{"NzbInfoUrl", "https://x/i"},
		{"DownloadUrl", "https://x/d"},
		{"DownloadClient", "QBittorrent"},
		{"DownloadClientName", "Local QB"},
		{"Guid", "guid-1"},
		{"Size", "1000000000"},
		{"TvdbId", "99999"},
		{"TvRageId", "12345"},
		{"ImdbId", "tt0001"},
		{"ReleaseGroup", "GROUP"},
		{"ReleaseHash", "abc123"},
		{"CustomFormatScore", "75"},
		{"SeriesMatchType", "Alias"},
		{"ReleaseSource", "InteractiveSearch"},
		{"ReleaseType", "0"},  // ParsedEpisodeInfo with no episode numbers + FullSeason=false → ReleaseTypeUnknown (int 0)
		{"IndexerFlags", "9"}, // Freeleech (1) + Internal (8) = 9
	}
	for _, c := range cases {
		if got := d[c.key]; got != c.want {
			t.Errorf("Data[%q] = %q, want %q", c.key, got, c.want)
		}
	}
	if _, ok := d["TorrentInfoHash"]; ok {
		t.Errorf("non-torrent release: TorrentInfoHash should be absent, got %q", d["TorrentInfoHash"])
	}
}

// TestHandleEpisodeGrabbed_ReleaseTypePopulated exercises the
// non-Unknown branches of ParsedEpisodeInfo.ReleaseType() — single
// episode, multi episode, and full-season pack — to confirm the
// grab handler now emits the computed value rather than a hardcoded
// "Unknown" (audit fix B.11).
func TestHandleEpisodeGrabbed_ReleaseTypePopulated(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		mutate  func(*parser.ParsedEpisodeInfo)
		wantStr string
	}{
		{"single_episode", func(p *parser.ParsedEpisodeInfo) { p.EpisodeNumbers = []int{1} }, "1"},
		{"multi_episode", func(p *parser.ParsedEpisodeInfo) { p.EpisodeNumbers = []int{1, 2} }, "2"},
		{"season_pack", func(p *parser.ParsedEpisodeInfo) { p.FullSeason = true }, "3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &stubWriter{}
			eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
			subject := grabbedSubject(eps, grabbedRelease())
			tc.mutate(subject.ParsedEpisodeInfo)
			ev := &decisionengine.EpisodeGrabbedEvent{Episode: subject}
			if err := historyhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
				t.Fatalf("HandleEpisodeGrabbed: %v", err)
			}
			if got := w.added[0].Data["ReleaseType"]; got != tc.wantStr {
				t.Errorf("ReleaseType: got %q, want %q", got, tc.wantStr)
			}
		})
	}
}

// TestHandleEpisodeGrabbed_ReleaseTypeNilPEI confirms the nil-guard
// keeps the field shape stable and emits the Unknown int form.
func TestHandleEpisodeGrabbed_ReleaseTypeNilPEI(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	subject := grabbedSubject(eps, grabbedRelease())
	subject.ParsedEpisodeInfo = nil
	ev := &decisionengine.EpisodeGrabbedEvent{Episode: subject}
	if err := historyhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	if got := w.added[0].Data["ReleaseType"]; got != "0" {
		t.Errorf("ReleaseType: got %q, want %q", got, "0")
	}
}

func TestHandleEpisodeGrabbed_TorrentInfoHash(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	rel := grabbedRelease()
	torrent := &parser.TorrentInfo{ReleaseInfo: *rel, InfoHash: "deadbeef"}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	ev := &decisionengine.EpisodeGrabbedEvent{
		Episode:    grabbedSubject(eps, torrent),
		DownloadID: "tok-1",
	}
	if err := historyhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	if len(w.added) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(w.added))
	}
	if got := w.added[0].Data["TorrentInfoHash"]; got != "deadbeef" {
		t.Errorf("TorrentInfoHash = %q, want %q", got, "deadbeef")
	}
}

func TestHandleEpisodeGrabbed_ReleaseHashAbsentWhenEmpty(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	subject := grabbedSubject(eps, grabbedRelease())
	subject.ParsedEpisodeInfo.ReleaseHash = ""
	ev := &decisionengine.EpisodeGrabbedEvent{Episode: subject}
	if err := historyhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	if _, ok := w.added[0].Data["ReleaseHash"]; ok {
		t.Errorf("ReleaseHash should be absent when empty")
	}
}

func TestHandleEpisodeGrabbed_LanguagesIDs(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	subject := grabbedSubject(eps, grabbedRelease())
	ev := &decisionengine.EpisodeGrabbedEvent{Episode: subject}
	if err := historyhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	got := w.added[0].Languages
	if len(got) != 2 || got[0] != language.English.ID || got[1] != 26 {
		t.Errorf("Languages = %v, want [%d, 26]", got, language.English.ID)
	}
}

func TestHandleEpisodeGrabbed_QualityJSON(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	subject := grabbedSubject(eps, grabbedRelease())
	ev := &decisionengine.EpisodeGrabbedEvent{Episode: subject}
	if err := historyhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	q := w.added[0].Quality
	if q == "" || q == "{}" {
		t.Errorf("Quality should be populated, got %q", q)
	}
}

func TestHandleEpisodeGrabbed_NilReleasePath(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	// Subject without a Release — fallback path. Per the C# tree's
	// posture, the foreach still inserts (no guard); the Go port
	// matches.
	subject := &decisionengine.Subject{
		Episodes: eps,
		Series:   &series.Series{ID: 7},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			Quality: quality.Model{Quality: quality.HDTV720p},
		},
	}
	ev := &decisionengine.EpisodeGrabbedEvent{Episode: subject}
	if err := historyhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	if len(w.added) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(w.added))
	}
	if w.added[0].SourceTitle != "" {
		t.Errorf("SourceTitle should be empty when Release is nil; got %q", w.added[0].SourceTitle)
	}
	if got := w.added[0].Data["Indexer"]; got != "" {
		t.Errorf("Indexer should be empty fallback; got %q", got)
	}
}

func TestHandleEpisodeGrabbed_PropagatesAddError(t *testing.T) {
	t.Parallel()
	w := &stubWriter{err: errors.New("repo down")}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}, {ID: 12, SeriesID: 7}}
	ev := &decisionengine.EpisodeGrabbedEvent{
		Episode:    grabbedSubject(eps, grabbedRelease()),
		DownloadID: "tok",
	}
	err := historyhandlers.HandleEpisodeGrabbed(t.Context(), w, ev)
	if err == nil {
		t.Fatalf("expected error from Add")
	}
}
