package historyhandlers_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/historyhandlers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// stubLookupDownloadID records every FindDownloadID call + returns
// the canned response. Tests assert on `calls` to confirm the
// fallback fires only when expected.
type stubLookupDownloadID struct {
	calls    []stubLookupCall
	respond  string
	respErr  error
	callable bool
}

type stubLookupCall struct {
	seriesID    int
	qualityName int
	episodeIDs  []int
}

func (s *stubLookupDownloadID) FindDownloadID(_ context.Context, seriesID int, q quality.Model, episodeIDs []int) (string, error) {
	s.callable = true
	s.calls = append(s.calls, stubLookupCall{
		seriesID:    seriesID,
		qualityName: q.Quality.ID,
		episodeIDs:  append([]int(nil), episodeIDs...),
	})
	if s.respErr != nil {
		return "", s.respErr
	}
	return s.respond, nil
}

func importedFile() *episodefiles.File {
	return &episodefiles.File{
		ID:           4242,
		SeriesID:     7,
		SceneName:    "Show.S01E01.1080p.BluRay-GROUP",
		RelativePath: "Season 01/Show.S01E01.mkv",
		IndexerFlags: int(parser.IndexerFlagFreeleech | parser.IndexerFlagInternal),
		ReleaseType:  int(parser.ReleaseTypeSingleEpisode),
	}
}

func importedLocalEpisode(eps []episodes.Episode) *episodeimport.LocalEpisode {
	return &episodeimport.LocalEpisode{
		Path:              "/tmp/dropfolder/Show.S01E01.1080p.BluRay-GROUP.mkv",
		Size:              1_500_000_000,
		Series:            &series.Series{ID: 7, Path: "/tv/Show"},
		Episodes:          eps,
		Quality:           quality.Model{Quality: quality.Bluray1080p, Revision: quality.Revision{Version: 2}},
		Languages:         []language.Language{language.English},
		ReleaseGroup:      "GROUP",
		CustomFormats:     []customformats.CustomFormat{},
		CustomFormatScore: 125,
	}
}

func importedDownloadItem() *downloadclients.Item {
	return &downloadclients.Item{
		DownloadID: "tok-import-1",
		DownloadClientInfo: &downloadclients.ItemClientInfo{
			Type: "Transmission",
			Name: "Local Transmission",
		},
	}
}

func TestHandleEpisodeImported_NilWriter(t *testing.T) {
	t.Parallel()
	if err := historyhandlers.HandleEpisodeImported(t.Context(), nil, &episodeimport.EpisodeImportedEvent{NewDownload: true}); err == nil {
		t.Errorf("expected error on nil writer")
	}
}

func TestHandleEpisodeImported_NoOpOnNilEvent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("nil event: writer should not be called; got %d", len(w.added))
	}
}

func TestHandleEpisodeImported_NoOpOnNotNewDownload(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode: importedLocalEpisode([]episodes.Episode{{ID: 11, SeriesID: 7}}),
		EpisodeFile:  importedFile(),
		NewDownload:  false,
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Errorf("not-new-download: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("not-new-download: writer should not be called; got %d", len(w.added))
	}
}

func TestHandleEpisodeImported_NoOpOnMissingPayload(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	cases := []*episodeimport.EpisodeImportedEvent{
		{NewDownload: true},
		{NewDownload: true, LocalEpisode: importedLocalEpisode(nil)},
		{NewDownload: true, EpisodeFile: importedFile()},
		{NewDownload: true, LocalEpisode: importedLocalEpisode([]episodes.Episode{}), EpisodeFile: importedFile()},
	}
	for i, ev := range cases {
		if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
			t.Errorf("case %d: %v", i, err)
		}
	}
	if len(w.added) != 0 {
		t.Errorf("missing payload: writer should not be called; got %d", len(w.added))
	}
}

func TestHandleEpisodeImported_WritesPerEpisode(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}, {ID: 12, SeriesID: 7}}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode:       importedLocalEpisode(eps),
		EpisodeFile:        importedFile(),
		NewDownload:        true,
		DownloadClientItem: importedDownloadItem(),
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
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
		if got.DownloadID != "tok-import-1" {
			t.Errorf("entry %d DownloadID = %q", i, got.DownloadID)
		}
		if got.EventType == nil || history.EventType(*got.EventType) != history.EventTypeDownloadFolderImported {
			t.Errorf("entry %d EventType = %v, want DownloadFolderImported", i, got.EventType)
		}
		if got.Date.IsZero() {
			t.Errorf("entry %d Date is zero", i)
		}
	}
}

func TestHandleEpisodeImported_SourceTitleFallsBackToFilename(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	f := importedFile()
	f.SceneName = ""
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode: importedLocalEpisode(eps),
		EpisodeFile:  f,
		NewDownload:  true,
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
	}
	if len(w.added) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(w.added))
	}
	if got := w.added[0].SourceTitle; got != "Show.S01E01.1080p.BluRay-GROUP" {
		t.Errorf("SourceTitle (fallback) = %q, want filename without extension", got)
	}
}

func TestHandleEpisodeImported_SourceTitleBlankSceneNameTreatedAsMissing(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	f := importedFile()
	f.SceneName = "   "
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode: importedLocalEpisode(eps),
		EpisodeFile:  f,
		NewDownload:  true,
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
	}
	if got := w.added[0].SourceTitle; got != "Show.S01E01.1080p.BluRay-GROUP" {
		t.Errorf("SourceTitle (whitespace fallback) = %q, want filename without extension", got)
	}
}

func TestHandleEpisodeImported_DataMapShape(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode:       importedLocalEpisode(eps),
		EpisodeFile:        importedFile(),
		NewDownload:        true,
		DownloadClientItem: importedDownloadItem(),
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
	}
	d := w.added[0].Data
	cases := []struct{ key, want string }{
		{"FileId", "4242"},
		{"DroppedPath", "/tmp/dropfolder/Show.S01E01.1080p.BluRay-GROUP.mkv"},
		{"ImportedPath", "/tv/Show/Season 01/Show.S01E01.mkv"},
		{"DownloadClient", "Transmission"},
		{"DownloadClientName", "Local Transmission"},
		{"ReleaseGroup", "GROUP"},
		{"CustomFormatScore", "125"},
		{"Size", "1500000000"},
		{"IndexerFlags", "Internal, Freeleech"},
		{"ReleaseType", "SingleEpisode"},
	}
	for _, c := range cases {
		if got := d[c.key]; got != c.want {
			t.Errorf("Data[%q] = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestHandleEpisodeImported_NoDownloadClientItem(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode: importedLocalEpisode(eps),
		EpisodeFile:  importedFile(),
		NewDownload:  true,
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
	}
	d := w.added[0].Data
	if d["DownloadClient"] != "" {
		t.Errorf("DownloadClient (no item) = %q, want empty", d["DownloadClient"])
	}
	if d["DownloadClientName"] != "" {
		t.Errorf("DownloadClientName (no item) = %q, want empty", d["DownloadClientName"])
	}
	if w.added[0].DownloadID != "" {
		t.Errorf("DownloadID (no item) = %q, want empty", w.added[0].DownloadID)
	}
}

func TestHandleEpisodeImported_DownloadClientItemMissingInfo(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode: importedLocalEpisode(eps),
		EpisodeFile:  importedFile(),
		NewDownload:  true,
		DownloadClientItem: &downloadclients.Item{
			DownloadID:         "tok-2",
			DownloadClientInfo: nil,
		},
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
	}
	if w.added[0].DownloadID != "tok-2" {
		t.Errorf("DownloadID = %q, want tok-2", w.added[0].DownloadID)
	}
	if got := w.added[0].Data["DownloadClient"]; got != "" {
		t.Errorf("DownloadClient (nil info) = %q, want empty", got)
	}
}

func TestHandleEpisodeImported_LanguagesPerEpisodeIndependent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}, {ID: 12, SeriesID: 7}}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode: importedLocalEpisode(eps),
		EpisodeFile:  importedFile(),
		NewDownload:  true,
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
	}
	if len(w.added) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(w.added))
	}
	if len(w.added[0].Languages) != 1 || w.added[0].Languages[0] != language.English.ID {
		t.Errorf("entry 0 Languages = %v", w.added[0].Languages)
	}
	w.added[0].Languages[0] = -999
	if w.added[1].Languages[0] == -999 {
		t.Errorf("Languages slice aliased between per-episode entries")
	}
}

func TestHandleEpisodeImported_AddErrorPropagates(t *testing.T) {
	t.Parallel()
	w := &stubWriter{err: errors.New("repo down")}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode: importedLocalEpisode(eps),
		EpisodeFile:  importedFile(),
		NewDownload:  true,
	}
	err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev)
	if err == nil {
		t.Fatal("expected error from stub writer")
	}
	if !errors.Is(err, w.err) {
		t.Errorf("error chain missing repo error: %v", err)
	}
}

func TestHandleEpisodeImported_QualityRoundTrip(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode: importedLocalEpisode(eps),
		EpisodeFile:  importedFile(),
		NewDownload:  true,
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
	}
	q := w.added[0].Quality
	var m quality.Model
	if err := m.UnmarshalJSON([]byte(q)); err != nil {
		t.Fatalf("Quality JSON did not round-trip: %v (raw=%q)", err, q)
	}
	if m.Quality.ID != quality.Bluray1080p.ID {
		t.Errorf("Quality.ID after round-trip = %d, want %d", m.Quality.ID, quality.Bluray1080p.ID)
	}
}

// --- WithDownloadIDLookup fallback (slice 5.J127) ------------------

func TestHandleEpisodeImported_LookupSkippedWhenDownloadIDPresent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	lookup := &stubLookupDownloadID{respond: "tok-from-lookup"}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode:       importedLocalEpisode(eps),
		EpisodeFile:        importedFile(),
		NewDownload:        true,
		DownloadClientItem: importedDownloadItem(),
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev,
		historyhandlers.WithDownloadIDLookup(lookup)); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
	}
	if w.added[0].DownloadID != "tok-import-1" {
		t.Errorf("DownloadID = %q, want tok-import-1 (event id should win)", w.added[0].DownloadID)
	}
	if lookup.callable {
		t.Errorf("lookup should not have been called; got %d calls", len(lookup.calls))
	}
}

func TestHandleEpisodeImported_LookupFillsEmptyDownloadID(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	lookup := &stubLookupDownloadID{respond: "tok-from-lookup"}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}, {ID: 12, SeriesID: 7}}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode: importedLocalEpisode(eps),
		EpisodeFile:  importedFile(),
		NewDownload:  true,
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev,
		historyhandlers.WithDownloadIDLookup(lookup)); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
	}
	if !lookup.callable || len(lookup.calls) != 1 {
		t.Fatalf("lookup calls = %d, want 1", len(lookup.calls))
	}
	got := lookup.calls[0]
	if got.seriesID != 7 {
		t.Errorf("lookup.seriesID = %d, want 7 (from ev.EpisodeFile.SeriesID)", got.seriesID)
	}
	if got.qualityName != quality.Bluray1080p.ID {
		t.Errorf("lookup.qualityID = %d, want %d", got.qualityName, quality.Bluray1080p.ID)
	}
	if len(got.episodeIDs) != 2 || got.episodeIDs[0] != 11 || got.episodeIDs[1] != 12 {
		t.Errorf("lookup.episodeIDs = %v, want [11 12]", got.episodeIDs)
	}
	for i, e := range w.added {
		if e.DownloadID != "tok-from-lookup" {
			t.Errorf("entry %d DownloadID = %q, want tok-from-lookup", i, e.DownloadID)
		}
	}
}

func TestHandleEpisodeImported_LookupErrorIsNonFatal(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	lookup := &stubLookupDownloadID{respErr: errors.New("repo down")}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode: importedLocalEpisode(eps),
		EpisodeFile:  importedFile(),
		NewDownload:  true,
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev,
		historyhandlers.WithDownloadIDLookup(lookup)); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
	}
	if len(w.added) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(w.added))
	}
	if w.added[0].DownloadID != "" {
		t.Errorf("DownloadID = %q, want empty (lookup error swallowed)", w.added[0].DownloadID)
	}
}

func TestHandleEpisodeImported_LookupEmptyReturnLeavesDownloadIDEmpty(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	lookup := &stubLookupDownloadID{respond: ""}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode: importedLocalEpisode(eps),
		EpisodeFile:  importedFile(),
		NewDownload:  true,
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev,
		historyhandlers.WithDownloadIDLookup(lookup)); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
	}
	if w.added[0].DownloadID != "" {
		t.Errorf("DownloadID = %q, want empty (lookup declined)", w.added[0].DownloadID)
	}
	if len(lookup.calls) != 1 {
		t.Errorf("lookup calls = %d, want 1", len(lookup.calls))
	}
}

func TestHandleEpisodeImported_WithDownloadIDLookupNilOptionIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode: importedLocalEpisode(eps),
		EpisodeFile:  importedFile(),
		NewDownload:  true,
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev,
		historyhandlers.WithDownloadIDLookup(nil)); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
	}
	if w.added[0].DownloadID != "" {
		t.Errorf("DownloadID = %q, want empty (no lookup wired)", w.added[0].DownloadID)
	}
}

func TestHandleEpisodeImported_LookupSkippedWhenWhitespaceDownloadIDFromEvent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	lookup := &stubLookupDownloadID{respond: "tok-from-lookup"}
	eps := []episodes.Episode{{ID: 11, SeriesID: 7}}
	ev := &episodeimport.EpisodeImportedEvent{
		LocalEpisode: importedLocalEpisode(eps),
		EpisodeFile:  importedFile(),
		NewDownload:  true,
		DownloadClientItem: &downloadclients.Item{
			DownloadID: "   ",
		},
	}
	if err := historyhandlers.HandleEpisodeImported(t.Context(), w, ev,
		historyhandlers.WithDownloadIDLookup(lookup)); err != nil {
		t.Fatalf("HandleEpisodeImported: %v", err)
	}
	if w.added[0].DownloadID != "tok-from-lookup" {
		t.Errorf("DownloadID = %q, want tok-from-lookup (whitespace id triggers fallback)", w.added[0].DownloadID)
	}
}

var _ historyhandlers.LookupDownloadID = (*history.Service)(nil)
