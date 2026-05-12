package xbmc_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadata/xbmc"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/tags"
)

// fakeTagLookup implements xbmc.TagLookup with an in-memory map.
type fakeTagLookup struct {
	tags map[int]string
	err  error
}

func (f *fakeTagLookup) GetByIDs(_ context.Context, ids []int) ([]tags.Tag, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]tags.Tag, 0, len(ids))
	for _, id := range ids {
		if label, ok := f.tags[id]; ok {
			out = append(out, tags.Tag{ID: id, Label: label})
		}
	}
	return out, nil
}

// tvshowXML mirrors the writer's emitted structure for parse-back
// verification. The test never byte-compares — Go's encoding/xml +
// .NET XDocument differ in trivial whitespace. Semantic equality is
// what Kodi cares about.
type tvshowXML struct {
	XMLName      xml.Name      `xml:"tvshow"`
	Title        string        `xml:"title"`
	Rating       *string       `xml:"rating,omitempty"`
	Plot         string        `xml:"plot"`
	Mpaa         string        `xml:"mpaa"`
	ID           int           `xml:"id"`
	UniqueIDs    []uniqueIDXML `xml:"uniqueid"`
	Genres       []string      `xml:"genre"`
	Tags         []string      `xml:"tag"`
	Status       string        `xml:"status"`
	Premiered    string        `xml:"premiered,omitempty"`
	EndDate      string        `xml:"enddate,omitempty"`
	Studio       string        `xml:"studio"`
	Actors       []actorXML    `xml:"actor"`
	EpisodeGuide string        `xml:"episodeguide,omitempty"`
}

type uniqueIDXML struct {
	Type    string `xml:"type,attr"`
	Default string `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}

type actorXML struct {
	Name  string `xml:"name"`
	Role  string `xml:"role"`
	Thumb string `xml:"thumb,omitempty"`
}

// parseSeriesMetadataXML extracts the XML root and any trailing URL
// suffix. SeriesMetadata may emit XML, URL, or XML + "\n" + URL.
func parseSeriesMetadataXML(t *testing.T, body string) (tvshowXML, string) {
	t.Helper()
	if !strings.HasPrefix(body, `<?xml`) {
		// URL-only body.
		return tvshowXML{}, body
	}
	// Find end of root close tag — the XML portion ends at the line
	// containing `</tvshow>`. Anything after is the URL suffix.
	idx := strings.Index(body, "</tvshow>")
	if idx < 0 {
		t.Fatalf("missing </tvshow> in body: %s", body)
	}
	xmlPart := body[:idx+len("</tvshow>")]
	suffix := strings.TrimLeft(body[idx+len("</tvshow>"):], "\n ")

	var parsed tvshowXML
	if err := xml.Unmarshal([]byte(xmlPart), &parsed); err != nil {
		t.Fatalf("parse XML: %v\nbody: %s", err, xmlPart)
	}
	return parsed, suffix
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(body)
}

func newSeriesMetadataXbmc(t *testing.T, settings xbmc.Settings, deps xbmc.Deps) *xbmc.XbmcMetadata {
	t.Helper()
	if deps.Detector == nil {
		deps.Detector = newDetectorFor(t, nil)
	}
	m, err := xbmc.NewXbmcMetadata(&metadata.Metadata{ID: 1}, settings, deps)
	if err != nil {
		t.Fatalf("NewXbmcMetadata: %v", err)
	}
	return m
}

func TestSeriesMetadata_HappyPath_AllFields(t *testing.T) {
	t.Parallel()
	firstAired := time.Date(2008, 1, 20, 0, 0, 0, 0, time.UTC)
	lastAired := time.Date(2013, 9, 29, 0, 0, 0, 0, time.UTC)
	s := &series.Series{
		ID:            42,
		TvdbID:        81189,
		ImdbID:        "tt0903747",
		TmdbID:        1396,
		TvMazeID:      169,
		Title:         "Breaking Bad",
		Overview:      "A chemistry teacher cooks meth.",
		Certification: "TV-MA",
		Network:       "AMC",
		Status:        series.SeriesStatusEnded,
		FirstAired:    &firstAired,
		LastAired:     &lastAired,
		Ratings:       mustMarshal(t, map[string]any{"votes": 1234, "value": 9.5}),
		Genres:        mustMarshal(t, []string{"Crime", "Drama", "Thriller"}),
		Tags:          []int{1, 2},
		Actors: mustMarshal(t, []map[string]any{
			{"name": "Bryan Cranston", "character": "Walter White", "images": []mediacover.Cover{{CoverType: mediacover.CoverTypeHeadshot, RemoteUrl: "https://cdn/bryan.jpg"}}},
			{"name": "Aaron Paul", "character": "Jesse Pinkman", "images": []mediacover.Cover{}},
		}),
	}
	tagLookup := &fakeTagLookup{tags: map[int]string{1: "favourite", 2: "complete"}}

	m := newSeriesMetadataXbmc(t, xbmc.DefaultSettings(), xbmc.Deps{Tags: tagLookup})

	res, err := m.SeriesMetadata(t.Context(), s, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("SeriesMetadata: %v", err)
	}
	if res.RelativePath != "tvshow.nfo" {
		t.Errorf("RelativePath = %q", res.RelativePath)
	}
	if !strings.HasPrefix(res.Contents, `<?xml`) {
		t.Fatalf("body should start with XML declaration: %q", res.Contents[:32])
	}

	parsed, suffix := parseSeriesMetadataXML(t, res.Contents)
	if suffix != "" {
		t.Errorf("default settings shouldn't include URL suffix, got %q", suffix)
	}
	if parsed.Title != "Breaking Bad" {
		t.Errorf("Title = %q", parsed.Title)
	}
	if parsed.Rating == nil || *parsed.Rating != "9.5" {
		t.Errorf("Rating = %v, want 9.5", parsed.Rating)
	}
	if parsed.Plot != "A chemistry teacher cooks meth." {
		t.Errorf("Plot = %q", parsed.Plot)
	}
	if parsed.Mpaa != "TV-MA" {
		t.Errorf("Mpaa = %q", parsed.Mpaa)
	}
	if parsed.ID != 81189 {
		t.Errorf("ID = %d", parsed.ID)
	}

	// 4 uniqueid elements: tvdb (default), imdb, tmdb, tvmaze.
	if len(parsed.UniqueIDs) != 4 {
		t.Fatalf("UniqueIDs = %d, want 4: %+v", len(parsed.UniqueIDs), parsed.UniqueIDs)
	}
	if parsed.UniqueIDs[0].Type != "tvdb" || parsed.UniqueIDs[0].Default != "true" || parsed.UniqueIDs[0].Value != "81189" {
		t.Errorf("UniqueIDs[0] = %+v", parsed.UniqueIDs[0])
	}
	if parsed.UniqueIDs[1].Type != "imdb" || parsed.UniqueIDs[1].Default != "" || parsed.UniqueIDs[1].Value != "tt0903747" {
		t.Errorf("UniqueIDs[1] = %+v", parsed.UniqueIDs[1])
	}
	if parsed.UniqueIDs[2].Type != "tmdb" || parsed.UniqueIDs[2].Value != "1396" {
		t.Errorf("UniqueIDs[2] = %+v", parsed.UniqueIDs[2])
	}
	if parsed.UniqueIDs[3].Type != "tvmaze" || parsed.UniqueIDs[3].Value != "169" {
		t.Errorf("UniqueIDs[3] = %+v", parsed.UniqueIDs[3])
	}

	wantGenres := []string{"Crime", "Drama", "Thriller"}
	if len(parsed.Genres) != len(wantGenres) {
		t.Errorf("Genres = %v, want %v", parsed.Genres, wantGenres)
	}
	for i, g := range wantGenres {
		if i < len(parsed.Genres) && parsed.Genres[i] != g {
			t.Errorf("Genres[%d] = %q, want %q", i, parsed.Genres[i], g)
		}
	}

	wantTags := []string{"favourite", "complete"}
	if len(parsed.Tags) != len(wantTags) {
		t.Errorf("Tags = %v, want %v", parsed.Tags, wantTags)
	}

	if parsed.Status != "Ended" {
		t.Errorf("Status = %q, want %q", parsed.Status, "Ended")
	}
	if parsed.Premiered != "2008-01-20" {
		t.Errorf("Premiered = %q", parsed.Premiered)
	}
	if parsed.EndDate != "2013-09-29" {
		t.Errorf("EndDate = %q (should be set for Ended series with LastAired)", parsed.EndDate)
	}
	if parsed.Studio != "AMC" {
		t.Errorf("Studio = %q", parsed.Studio)
	}

	if len(parsed.Actors) != 2 {
		t.Fatalf("Actors = %+v", parsed.Actors)
	}
	if parsed.Actors[0].Name != "Bryan Cranston" || parsed.Actors[0].Role != "Walter White" || parsed.Actors[0].Thumb != "https://cdn/bryan.jpg" {
		t.Errorf("Actors[0] = %+v", parsed.Actors[0])
	}
	if parsed.Actors[1].Name != "Aaron Paul" || parsed.Actors[1].Thumb != "" {
		t.Errorf("Actors[1] no-image case: %+v", parsed.Actors[1])
	}

	// EpisodeGuide default false → not emitted.
	if parsed.EpisodeGuide != "" {
		t.Errorf("EpisodeGuide should be empty when SeriesMetadataEpisodeGuide=false: %q", parsed.EpisodeGuide)
	}
}

func TestSeriesMetadata_RatingsGate(t *testing.T) {
	t.Parallel()
	// Votes==0 → no <rating> element.
	s := &series.Series{
		ID: 1, TvdbID: 999, Title: "Foo",
		Ratings: mustMarshal(t, map[string]any{"votes": 0, "value": 7.5}),
	}
	m := newSeriesMetadataXbmc(t, xbmc.DefaultSettings(), xbmc.Deps{})
	res, err := m.SeriesMetadata(t.Context(), s, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("%v", err)
	}
	parsed, _ := parseSeriesMetadataXML(t, res.Contents)
	if parsed.Rating != nil {
		t.Errorf("Rating should be omitted when Votes==0: %v", parsed.Rating)
	}
}

func TestSeriesMetadata_UniqueIDsNullableGates(t *testing.T) {
	t.Parallel()
	// All three optional uniqueid sources unset → only tvdb emitted.
	s := &series.Series{ID: 1, TvdbID: 100, Title: "Foo"}
	m := newSeriesMetadataXbmc(t, xbmc.DefaultSettings(), xbmc.Deps{})
	res, err := m.SeriesMetadata(t.Context(), s, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("%v", err)
	}
	parsed, _ := parseSeriesMetadataXML(t, res.Contents)
	if len(parsed.UniqueIDs) != 1 || parsed.UniqueIDs[0].Type != "tvdb" {
		t.Errorf("UniqueIDs = %+v, want only tvdb", parsed.UniqueIDs)
	}
}

func TestSeriesMetadata_EndDateGate(t *testing.T) {
	t.Parallel()
	lastAired := time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC)

	// Continuing series with LastAired → no enddate.
	s1 := &series.Series{ID: 1, TvdbID: 1, Title: "X", Status: series.SeriesStatusContinuing, LastAired: &lastAired}
	m := newSeriesMetadataXbmc(t, xbmc.DefaultSettings(), xbmc.Deps{})
	res, _ := m.SeriesMetadata(t.Context(), s1, metadata.SeriesMetadataReasonScan)
	parsed, _ := parseSeriesMetadataXML(t, res.Contents)
	if parsed.EndDate != "" {
		t.Errorf("Continuing+LastAired: EndDate = %q, want empty", parsed.EndDate)
	}

	// Ended series without LastAired → no enddate.
	s2 := &series.Series{ID: 1, TvdbID: 1, Title: "X", Status: series.SeriesStatusEnded}
	res, _ = m.SeriesMetadata(t.Context(), s2, metadata.SeriesMetadataReasonScan)
	parsed, _ = parseSeriesMetadataXML(t, res.Contents)
	if parsed.EndDate != "" {
		t.Errorf("Ended+nilLastAired: EndDate = %q, want empty", parsed.EndDate)
	}
}

func TestSeriesMetadata_EpisodeGuideGate(t *testing.T) {
	t.Parallel()
	settings := xbmc.DefaultSettings()
	settings.SeriesMetadataEpisodeGuide = true
	s := &series.Series{ID: 1, TvdbID: 12345, ImdbID: "tt0012345", Title: "Foo"}
	m := newSeriesMetadataXbmc(t, settings, xbmc.Deps{})
	res, err := m.SeriesMetadata(t.Context(), s, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("%v", err)
	}
	parsed, _ := parseSeriesMetadataXML(t, res.Contents)
	if parsed.EpisodeGuide == "" {
		t.Fatal("EpisodeGuide should be populated")
	}
	// Should be valid JSON.
	var guide struct {
		Tvdb string `json:"tvdb"`
		Imdb string `json:"imdb"`
	}
	if err := json.Unmarshal([]byte(parsed.EpisodeGuide), &guide); err != nil {
		t.Fatalf("EpisodeGuide not JSON: %v\nbody: %q", err, parsed.EpisodeGuide)
	}
	if guide.Tvdb != "12345" || guide.Imdb != "tt0012345" {
		t.Errorf("EpisodeGuide content: %+v", guide)
	}
}

func TestSeriesMetadata_URLSuffix(t *testing.T) {
	t.Parallel()
	// URL only.
	urlOnly := xbmc.DefaultSettings()
	urlOnly.SeriesMetadata = false
	urlOnly.SeriesMetadataUrl = true
	s := &series.Series{ID: 1, TvdbID: 999, Title: "X"}
	m := newSeriesMetadataXbmc(t, urlOnly, xbmc.Deps{})
	res, err := m.SeriesMetadata(t.Context(), s, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("%v", err)
	}
	want := "https://www.thetvdb.com/?tab=series&id=999"
	if res.Contents != want {
		t.Errorf("URL-only body = %q, want %q", res.Contents, want)
	}

	// XML + URL.
	both := xbmc.DefaultSettings()
	both.SeriesMetadataUrl = true
	m = newSeriesMetadataXbmc(t, both, xbmc.Deps{})
	res, err = m.SeriesMetadata(t.Context(), s, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("%v", err)
	}
	parsed, suffix := parseSeriesMetadataXML(t, res.Contents)
	if parsed.Title != "X" {
		t.Errorf("parsed Title = %q", parsed.Title)
	}
	if suffix != want {
		t.Errorf("XML+URL suffix = %q, want %q", suffix, want)
	}
}

func TestSeriesMetadata_BothGatesOff(t *testing.T) {
	t.Parallel()
	settings := xbmc.DefaultSettings()
	settings.SeriesMetadata = false
	settings.SeriesMetadataUrl = false
	m := newSeriesMetadataXbmc(t, settings, xbmc.Deps{})
	res, err := m.SeriesMetadata(t.Context(), &series.Series{ID: 1, TvdbID: 1, Title: "X"}, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if res.RelativePath != "" || res.Contents != "" {
		t.Errorf("both gates off: res=%+v, want zero-value", res)
	}
}

func TestSeriesMetadata_EpisodesImportedReasonSkips(t *testing.T) {
	t.Parallel()
	// Reason gate at the top of the method.
	m := newSeriesMetadataXbmc(t, xbmc.DefaultSettings(), xbmc.Deps{})
	res, err := m.SeriesMetadata(t.Context(), &series.Series{ID: 1, TvdbID: 1, Title: "X"}, metadata.SeriesMetadataReasonEpisodesImported)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if res.RelativePath != "" || res.Contents != "" {
		t.Errorf("EpisodesImported reason should skip: %+v", res)
	}
}

func TestSeriesMetadata_NilTagLookupSkipsTagBlock(t *testing.T) {
	t.Parallel()
	s := &series.Series{ID: 1, TvdbID: 1, Title: "X", Tags: []int{1, 2}}
	m := newSeriesMetadataXbmc(t, xbmc.DefaultSettings(), xbmc.Deps{})
	res, err := m.SeriesMetadata(t.Context(), s, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("%v", err)
	}
	parsed, _ := parseSeriesMetadataXML(t, res.Contents)
	if len(parsed.Tags) != 0 {
		t.Errorf("nil TagLookup but Tags emitted: %v", parsed.Tags)
	}
}

func TestSeriesMetadata_TagLookupErrorIsNonFatal(t *testing.T) {
	t.Parallel()
	s := &series.Series{ID: 1, TvdbID: 1, Title: "X", Tags: []int{1}}
	deps := xbmc.Deps{Tags: &fakeTagLookup{err: errors.New("db down")}}
	m := newSeriesMetadataXbmc(t, xbmc.DefaultSettings(), deps)
	res, err := m.SeriesMetadata(t.Context(), s, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("TagLookup error should be non-fatal: %v", err)
	}
	parsed, _ := parseSeriesMetadataXML(t, res.Contents)
	if len(parsed.Tags) != 0 {
		t.Errorf("Tags should be empty after lookup error: %v", parsed.Tags)
	}
	if parsed.Title != "X" {
		t.Errorf("Title still rendered: %q", parsed.Title)
	}
}

func TestSeriesMetadata_MalformedRatingsErrors(t *testing.T) {
	t.Parallel()
	s := &series.Series{ID: 1, TvdbID: 1, Title: "X", Ratings: "{not json"}
	m := newSeriesMetadataXbmc(t, xbmc.DefaultSettings(), xbmc.Deps{})
	_, err := m.SeriesMetadata(t.Context(), s, metadata.SeriesMetadataReasonScan)
	if err == nil {
		t.Errorf("expected error on malformed ratings JSON")
	}
}

func TestSeriesMetadata_StatusVariants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status int
		want   string
	}{
		{series.SeriesStatusContinuing, "Continuing"},
		{series.SeriesStatusEnded, "Ended"},
		{series.SeriesStatusUpcoming, "Upcoming"},
		{series.SeriesStatusDeleted, "Deleted"},
		{999, ""},
	}
	m := newSeriesMetadataXbmc(t, xbmc.DefaultSettings(), xbmc.Deps{})
	for _, tc := range cases {
		s := &series.Series{ID: 1, TvdbID: 1, Title: "X", Status: tc.status}
		res, _ := m.SeriesMetadata(t.Context(), s, metadata.SeriesMetadataReasonScan)
		parsed, _ := parseSeriesMetadataXML(t, res.Contents)
		if parsed.Status != tc.want {
			t.Errorf("status=%d: got %q, want %q", tc.status, parsed.Status, tc.want)
		}
	}
}

func TestSeriesMetadata_XMLDeclaration(t *testing.T) {
	t.Parallel()
	m := newSeriesMetadataXbmc(t, xbmc.DefaultSettings(), xbmc.Deps{})
	s := &series.Series{ID: 1, TvdbID: 1, Title: "X"}
	res, _ := m.SeriesMetadata(t.Context(), s, metadata.SeriesMetadataReasonScan)
	wantDecl := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`
	if !strings.HasPrefix(res.Contents, wantDecl) {
		t.Errorf("XML declaration mismatch:\n got: %q\nwant: %q", res.Contents[:len(wantDecl)+5], wantDecl)
	}
}

func TestSeriesMetadata_NilReceiver(t *testing.T) {
	t.Parallel()
	var m *xbmc.XbmcMetadata
	res, err := m.SeriesMetadata(t.Context(), &series.Series{}, metadata.SeriesMetadataReasonScan)
	if err != nil || res.RelativePath != "" {
		t.Errorf("nil receiver should return zero-value: %+v / %v", res, err)
	}
}

func TestSeriesMetadata_NilSeries(t *testing.T) {
	t.Parallel()
	m := newSeriesMetadataXbmc(t, xbmc.DefaultSettings(), xbmc.Deps{})
	res, err := m.SeriesMetadata(t.Context(), nil, metadata.SeriesMetadataReasonScan)
	if err != nil || res.RelativePath != "" {
		t.Errorf("nil series should return zero-value: %+v / %v", res, err)
	}
}
