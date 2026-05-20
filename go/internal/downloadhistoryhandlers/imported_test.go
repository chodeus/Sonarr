package downloadhistoryhandlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/downloadhistoryhandlers"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

type stubLookup struct {
	mu       sync.Mutex
	calls    int
	respond  string
	err      error
	gotEpIDs []int
}

func (s *stubLookup) FindDownloadID(_ context.Context, _ int, _ quality.Model, episodeIDs []int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.gotEpIDs = append([]int(nil), episodeIDs...)
	if s.err != nil {
		return "", s.err
	}
	return s.respond, nil
}

func importedFile() *episodefiles.File {
	return &episodefiles.File{
		ID:           4242,
		SeriesID:     7,
		RelativePath: "Season 01/Show.S01E01.mkv",
	}
}

func importedLocalEpisode(eps []episodes.Episode) *episodeimport.LocalEpisode {
	return &episodeimport.LocalEpisode{
		Path:     "/tmp/dropfolder/Show.S01E01.mkv",
		Series:   &series.Series{ID: 7, Path: "/tv/Show"},
		Episodes: eps,
		Quality:  quality.Model{Quality: quality.Bluray1080p},
	}
}

func importedClientItem() *downloadclients.Item {
	return &downloadclients.Item{
		DownloadID: "tok-import-1",
		DownloadClientInfo: &downloadclients.ItemClientInfo{
			ID:       17,
			Type:     "Sabnzbd",
			Name:     "Main Sabnzbd",
			Protocol: indexers.DownloadProtocolUsenet,
		},
	}
}

func TestImported_NilWriter(t *testing.T) {
	t.Parallel()
	if err := downloadhistoryhandlers.HandleEpisodeImported(t.Context(), nil,
		&episodeimport.EpisodeImportedEvent{NewDownload: true}); err == nil {
		t.Errorf("expected error on nil writer")
	}
}

func TestImported_NilEventIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleEpisodeImported(t.Context(), w, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("writer should not be called; got %d", len(w.added))
	}
}

func TestImported_NewDownloadFalseIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := &episodeimport.EpisodeImportedEvent{
		NewDownload:        false,
		LocalEpisode:       importedLocalEpisode([]episodes.Episode{{ID: 11, SeriesID: 7}}),
		EpisodeFile:        importedFile(),
		DownloadClientItem: importedClientItem(),
	}
	if err := downloadhistoryhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Errorf("not-new-download: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("writer should not be called when NewDownload=false; got %d", len(w.added))
	}
}

func TestImported_MissingPayloadIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	cases := []*episodeimport.EpisodeImportedEvent{
		{NewDownload: true},
		{NewDownload: true, LocalEpisode: importedLocalEpisode(nil)},
		{NewDownload: true, EpisodeFile: importedFile()},
	}
	for i, ev := range cases {
		if err := downloadhistoryhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
			t.Errorf("case %d: %v", i, err)
		}
	}
	if len(w.added) != 0 {
		t.Errorf("missing payload: writer should not be called; got %d", len(w.added))
	}
}

func TestImported_EmptyDownloadIDNoFallbackIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := &episodeimport.EpisodeImportedEvent{
		NewDownload:  true,
		LocalEpisode: importedLocalEpisode([]episodes.Episode{{ID: 11}}),
		EpisodeFile:  importedFile(),
	}
	if err := downloadhistoryhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("empty downloadID + no fallback: expected no row; got %d", len(w.added))
	}
}

func TestImported_WritesEntryFromEventID(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := &episodeimport.EpisodeImportedEvent{
		NewDownload:        true,
		LocalEpisode:       importedLocalEpisode([]episodes.Episode{{ID: 11, SeriesID: 7}, {ID: 12}}),
		EpisodeFile:        importedFile(),
		DownloadClientItem: importedClientItem(),
	}
	if err := downloadhistoryhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.added) != 1 {
		t.Fatalf("expected 1 entry (per-event, not per-episode); got %d", len(w.added))
	}
	got := w.added[0]
	if downloadhistory.EventType(got.EventType) != downloadhistory.EventTypeFileImported {
		t.Errorf("EventType = %v, want FileImported", got.EventType)
	}
	if got.SeriesID != 7 {
		t.Errorf("SeriesID = %d, want 7 (from ev.EpisodeFile.SeriesID)", got.SeriesID)
	}
	if got.DownloadID != "tok-import-1" {
		t.Errorf("DownloadID = %q", got.DownloadID)
	}
	if got.SourceTitle != "/tmp/dropfolder/Show.S01E01.mkv" {
		t.Errorf("SourceTitle = %q (must equal LocalEpisode.Path)", got.SourceTitle)
	}
	if got.Protocol != int(indexers.DownloadProtocolUsenet) {
		t.Errorf("Protocol = %d", got.Protocol)
	}
	if got.DownloadClientID != 17 {
		t.Errorf("DownloadClientID = %d, want 17", got.DownloadClientID)
	}
}

func TestImported_DataMapShape(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := &episodeimport.EpisodeImportedEvent{
		NewDownload:        true,
		LocalEpisode:       importedLocalEpisode([]episodes.Episode{{ID: 11}}),
		EpisodeFile:        importedFile(),
		DownloadClientItem: importedClientItem(),
	}
	if err := downloadhistoryhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	var data map[string]string
	if err := json.Unmarshal([]byte(w.added[0].Data), &data); err != nil {
		t.Fatalf("Data JSON did not round-trip: %v", err)
	}
	cases := []struct{ key, want string }{
		{"DownloadClient", "Sabnzbd"},
		{"DownloadClientName", "Main Sabnzbd"},
		{"SourcePath", "/tmp/dropfolder/Show.S01E01.mkv"},
		{"DestinationPath", "/tv/Show/Season 01/Show.S01E01.mkv"},
	}
	for _, c := range cases {
		if got := data[c.key]; got != c.want {
			t.Errorf("Data[%q] = %q, want %q", c.key, got, c.want)
		}
	}
	if len(data) != 4 {
		t.Errorf("Data has %d keys, want 4; got %v", len(data), data)
	}
}

func TestImported_FallbackFillsEmptyDownloadID(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	lookup := &stubLookup{respond: "tok-lookup"}
	ev := &episodeimport.EpisodeImportedEvent{
		NewDownload:  true,
		LocalEpisode: importedLocalEpisode([]episodes.Episode{{ID: 11}, {ID: 12}}),
		EpisodeFile:  importedFile(),
	}
	if err := downloadhistoryhandlers.HandleEpisodeImported(t.Context(), w, ev,
		downloadhistoryhandlers.WithDownloadIDLookup(lookup)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if lookup.calls != 1 {
		t.Errorf("lookup calls = %d, want 1", lookup.calls)
	}
	if len(lookup.gotEpIDs) != 2 || lookup.gotEpIDs[0] != 11 || lookup.gotEpIDs[1] != 12 {
		t.Errorf("lookup episodeIDs = %v, want [11 12]", lookup.gotEpIDs)
	}
	if len(w.added) != 1 || w.added[0].DownloadID != "tok-lookup" {
		t.Errorf("DownloadID = %v, want tok-lookup", w.added)
	}
}

func TestImported_FallbackDeclinesLeavesNoRow(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	lookup := &stubLookup{respond: ""}
	ev := &episodeimport.EpisodeImportedEvent{
		NewDownload:  true,
		LocalEpisode: importedLocalEpisode([]episodes.Episode{{ID: 11}}),
		EpisodeFile:  importedFile(),
	}
	if err := downloadhistoryhandlers.HandleEpisodeImported(t.Context(), w, ev,
		downloadhistoryhandlers.WithDownloadIDLookup(lookup)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("lookup-declined: expected no row; got %d", len(w.added))
	}
}

func TestImported_FallbackErrorIsNonFatalAndNoRow(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	lookup := &stubLookup{err: errors.New("repo down")}
	ev := &episodeimport.EpisodeImportedEvent{
		NewDownload:  true,
		LocalEpisode: importedLocalEpisode([]episodes.Episode{{ID: 11}}),
		EpisodeFile:  importedFile(),
	}
	if err := downloadhistoryhandlers.HandleEpisodeImported(t.Context(), w, ev,
		downloadhistoryhandlers.WithDownloadIDLookup(lookup)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("lookup-errored: expected no row; got %d", len(w.added))
	}
}

func TestImported_LookupSkippedWhenEventCarriesID(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	lookup := &stubLookup{respond: "tok-lookup"}
	ev := &episodeimport.EpisodeImportedEvent{
		NewDownload:        true,
		LocalEpisode:       importedLocalEpisode([]episodes.Episode{{ID: 11}}),
		EpisodeFile:        importedFile(),
		DownloadClientItem: importedClientItem(),
	}
	if err := downloadhistoryhandlers.HandleEpisodeImported(t.Context(), w, ev,
		downloadhistoryhandlers.WithDownloadIDLookup(lookup)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if lookup.calls != 0 {
		t.Errorf("lookup should not be called when event has DownloadID; got %d", lookup.calls)
	}
	if w.added[0].DownloadID != "tok-import-1" {
		t.Errorf("DownloadID = %q, want tok-import-1 (event id wins)", w.added[0].DownloadID)
	}
}

func TestImported_NilClientInfoZeroFallback(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := &episodeimport.EpisodeImportedEvent{
		NewDownload:  true,
		LocalEpisode: importedLocalEpisode([]episodes.Episode{{ID: 11}}),
		EpisodeFile:  importedFile(),
		DownloadClientItem: &downloadclients.Item{
			DownloadID:         "tok-no-info",
			DownloadClientInfo: nil,
		},
	}
	if err := downloadhistoryhandlers.HandleEpisodeImported(t.Context(), w, ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	got := w.added[0]
	if got.Protocol != 0 || got.DownloadClientID != 0 {
		t.Errorf("nil DownloadClientInfo should yield zero Protocol+DownloadClientID; got %+v", got)
	}
	var data map[string]string
	_ = json.Unmarshal([]byte(got.Data), &data)
	if data["DownloadClient"] != "" || data["DownloadClientName"] != "" {
		t.Errorf("nil DownloadClientInfo should yield empty Data[DownloadClient/Name]; got %v", data)
	}
}

func TestImported_AddErrorPropagates(t *testing.T) {
	t.Parallel()
	w := &stubWriter{err: errors.New("repo down")}
	ev := &episodeimport.EpisodeImportedEvent{
		NewDownload:        true,
		LocalEpisode:       importedLocalEpisode([]episodes.Episode{{ID: 11}}),
		EpisodeFile:        importedFile(),
		DownloadClientItem: importedClientItem(),
	}
	err := downloadhistoryhandlers.HandleEpisodeImported(t.Context(), w, ev)
	if err == nil {
		t.Fatal("expected error from stub writer")
	}
	if !errors.Is(err, w.err) {
		t.Errorf("error chain missing source: %v", err)
	}
}

// Compile-time assertion: *history.Service satisfies LookupDownloadID.
var _ downloadhistoryhandlers.LookupDownloadID = (*history.Service)(nil)
