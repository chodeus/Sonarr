package xbmc_test

import (
	"encoding/json"
	"testing"

	"sonarr.tv/sonarr/internal/metadata/xbmc"
	"sonarr.tv/sonarr/internal/series"
)

func TestNewKodiEpisodeGuide_AllIDs(t *testing.T) {
	t.Parallel()
	g := xbmc.NewKodiEpisodeGuide(&series.Series{
		TvdbID:   81189,
		TvMazeID: 169,
		TvRageID: 18164,
		TmdbID:   1396,
		ImdbID:   "tt0903747",
	})
	if g.Tvdb != "81189" {
		t.Errorf("Tvdb = %q", g.Tvdb)
	}
	if g.TvMaze == nil || *g.TvMaze != "169" {
		t.Errorf("TvMaze = %v", g.TvMaze)
	}
	// PRESERVED-C#-BUG: TvRage carries TvMazeId value when TvRageId
	// is non-zero. See KodiEpisodeGuide.cs:31.
	if g.TvRage == nil || *g.TvRage != "169" {
		t.Errorf("TvRage (preserved-bug) = %v, want %q", g.TvRage, "169")
	}
	if g.Tmdb == nil || *g.Tmdb != "1396" {
		t.Errorf("Tmdb = %v", g.Tmdb)
	}
	if g.Imdb == nil || *g.Imdb != "tt0903747" {
		t.Errorf("Imdb = %v", g.Imdb)
	}
}

func TestNewKodiEpisodeGuide_GatedNullables(t *testing.T) {
	t.Parallel()
	g := xbmc.NewKodiEpisodeGuide(&series.Series{
		TvdbID:   81189,
		TvMazeID: 0,
		TvRageID: 0,
		TmdbID:   0,
		ImdbID:   "",
	})
	if g.Tvdb != "81189" {
		t.Errorf("Tvdb always set: got %q", g.Tvdb)
	}
	if g.TvMaze != nil {
		t.Errorf("TvMaze should be nil when ID=0: %v", *g.TvMaze)
	}
	if g.TvRage != nil {
		t.Errorf("TvRage should be nil when ID=0: %v", *g.TvRage)
	}
	if g.Tmdb != nil {
		t.Errorf("Tmdb should be nil when ID=0: %v", *g.Tmdb)
	}
	// Imdb is unconditionally assigned per C# :33 — empty string
	// becomes a pointer to "" (NOT nil), preserving byte parity.
	if g.Imdb == nil || *g.Imdb != "" {
		t.Errorf("Imdb should be ptr-to-empty when ImdbID is empty: %v", g.Imdb)
	}
}

func TestNewKodiEpisodeGuide_NilSeries(t *testing.T) {
	t.Parallel()
	g := xbmc.NewKodiEpisodeGuide(nil)
	if g.Tvdb != "" || g.TvMaze != nil || g.TvRage != nil || g.Tmdb != nil || g.Imdb != nil {
		t.Errorf("nil series should produce zero value, got %+v", g)
	}
}

func TestKodiEpisodeGuide_JSON_PopulatedSeries(t *testing.T) {
	t.Parallel()
	g := xbmc.NewKodiEpisodeGuide(&series.Series{
		TvdbID:   81189,
		TvMazeID: 169,
		TmdbID:   1396,
		ImdbID:   "tt0903747",
	})
	body, err := json.Marshal(&g)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Field key names match C# JsonPropertyName attributes.
	// Order is map-key iteration so we assert via decoded form.
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if raw["tvdb"] != "81189" {
		t.Errorf("tvdb = %v", raw["tvdb"])
	}
	if raw["tvmaze"] != "169" {
		t.Errorf("tvmaze = %v", raw["tvmaze"])
	}
	// TvRageID was zero — TvRage is nil → emits `null` (Option A).
	if v, ok := raw["tvrage"]; !ok || v != nil {
		t.Errorf("tvrage should be null when TvRageID was 0: ok=%v v=%v", ok, v)
	}
	if raw["tmdb"] != "1396" {
		t.Errorf("tmdb = %v", raw["tmdb"])
	}
	if raw["imdb"] != "tt0903747" {
		t.Errorf("imdb = %v", raw["imdb"])
	}
}

func TestKodiEpisodeGuide_JSON_FieldNamesAndCount(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(&xbmc.KodiEpisodeGuide{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, k := range []string{"tvdb", "tvmaze", "tvrage", "tmdb", "imdb"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing JSON key %q (Option A: every field emitted)", k)
		}
	}
	if len(raw) != 5 {
		t.Errorf("got %d keys (%v), want 5", len(raw), raw)
	}
}
