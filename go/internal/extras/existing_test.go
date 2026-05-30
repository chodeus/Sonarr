package extras

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

// stubResolver maps a path to a fixed (fileID, season, ok) by lookup.
type stubResolver struct {
	byPath map[string]struct {
		fileID, season int
		ok             bool
	}
}

func (s stubResolver) ResolveEpisodeFile(_ context.Context, _ *series.Series, path string) (int, int, bool) {
	r := s.byPath[path]
	return r.fileID, r.season, r.ok
}

func TestExistingExtraService_NilDeps(t *testing.T) {
	t.Parallel()
	if _, err := NewExistingExtraService(nil, &stubExtraStore{}, stubResolver{}, nil); err == nil {
		t.Error("expected error on nil subStore")
	}
}

func TestExistingExtraService_ImportExisting(t *testing.T) {
	t.Parallel()
	subStore := &stubSubStore{}
	extraStore := &stubExtraStore{}
	resolver := stubResolver{byPath: map[string]struct {
		fileID, season int
		ok             bool
	}{
		"/tv/Show/Season 01/Show.S01E02.en.srt": {fileID: 55, season: 1, ok: true},
		"/tv/Show/Season 01/Show.S01E02.nfo":    {fileID: 55, season: 1, ok: true},
		"/tv/Show/Season 01/unmatched.srt":      {ok: false},
	}}
	svc, err := NewExistingExtraService(subStore, extraStore, resolver, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewExistingExtraService: %v", err)
	}

	n, err := svc.ImportExisting(context.Background(), &series.Series{ID: 7, Path: "/tv/Show"}, []string{
		"/tv/Show/Season 01/Show.S01E02.en.srt", // subtitle → subtitle store
		"/tv/Show/Season 01/Show.S01E02.nfo",    // other → extra store
		"/tv/Show/Season 01/unmatched.srt",      // unresolved → skipped
		"/tv/Show/Season 01/noext",              // no extension → skipped
	})
	if err != nil || n != 2 {
		t.Fatalf("ImportExisting = (%d,%v), want (2,nil)", n, err)
	}
	if len(subStore.rows) != 1 || subStore.rows[0].RelativePath != "Season 01/Show.S01E02.en.srt" || subStore.rows[0].EpisodeFileID != 55 {
		t.Errorf("subtitle row = %+v", subStore.rows)
	}
	if len(extraStore.rows) != 1 || extraStore.rows[0].RelativePath != "Season 01/Show.S01E02.nfo" || extraStore.rows[0].Extension != ".nfo" {
		t.Errorf("extra row = %+v", extraStore.rows)
	}
}
