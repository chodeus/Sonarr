package metadatasource

import (
	"encoding/json"
	"testing"
	"time"
)

// TestShowResource_UnmarshalSmoke decodes a hand-shaped JSON payload
// that exercises every nullable, every sub-object, and every slice
// shape on ShowResource so the field tags survive future refactors.
//
// The payload is **not** a recorded SkyHook response — it's a minimal
// synthetic shape that mirrors the C# wire contract documented in
// `go/analysis/providers/metadata-sources.md`. The slice-4.AT
// (queue [M3a]) port will add goldenfile fixtures recorded from the
// Phase 0.5 contract corpus; that's where field-by-field parity
// against a known TVDB ID lives. This test is just "the JSON tags
// match the documented shape".
func TestShowResource_UnmarshalSmoke(t *testing.T) {
	payload := []byte(`{
		"tvdbId": 81189,
		"title": "Breaking Bad",
		"overview": "A high school chemistry teacher...",
		"slug": "breaking-bad",
		"firstAired": "2008-01-20",
		"lastAired": "2013-09-29",
		"tvRageId": 18164,
		"tvMazeId": 169,
		"tmdbId": 1396,
		"malIds": [9876, 9877],
		"aniListIds": [55555],
		"status": "ended",
		"runtime": 47,
		"timeOfDay": {"hours": 22, "minutes": 0},
		"network": "AMC",
		"imdbId": "tt0903747",
		"originalLanguage": "eng",
		"originalCountry": "usa",
		"actors": [{"name": "Bryan Cranston", "character": "Walter White", "image": "https://image/wc.jpg"}],
		"genres": ["Drama", "Thriller"],
		"contentRating": "TV-MA",
		"rating": {"count": 12345, "value": 9.4},
		"images": [
			{"coverType": "poster", "url": "https://image/poster.jpg"},
			{"coverType": "fanart", "url": "https://image/fanart.jpg"}
		],
		"seasons": [
			{"seasonNumber": 0, "images": []},
			{"seasonNumber": 1, "images": [{"coverType": "poster", "url": "https://image/s1.jpg"}]}
		],
		"episodes": [
			{
				"tvdbId": 349231,
				"seasonNumber": 1,
				"episodeNumber": 1,
				"absoluteEpisodeNumber": 1,
				"airedAfterSeasonNumber": null,
				"airedBeforeSeasonNumber": null,
				"airedBeforeEpisodeNumber": null,
				"title": "Pilot",
				"airDate": "2008-01-20",
				"airDateUtc": "2008-01-21T03:00:00Z",
				"runtime": 58,
				"finaleType": null,
				"rating": {"count": 100, "value": 8.7},
				"overview": "Walt cooks his first batch.",
				"image": "https://image/ep1.jpg"
			}
		]
	}`)

	var show ShowResource
	if err := json.Unmarshal(payload, &show); err != nil {
		t.Fatalf("unmarshal show: %v", err)
	}

	if show.TvdbID != 81189 {
		t.Errorf("TvdbID = %d, want 81189", show.TvdbID)
	}
	if show.Title != "Breaking Bad" {
		t.Errorf("Title = %q, want Breaking Bad", show.Title)
	}
	if show.TvRageID == nil || *show.TvRageID != 18164 {
		t.Errorf("TvRageID = %v, want *18164", show.TvRageID)
	}
	if show.TvMazeID == nil || *show.TvMazeID != 169 {
		t.Errorf("TvMazeID = %v, want *169", show.TvMazeID)
	}
	if show.TmdbID == nil || *show.TmdbID != 1396 {
		t.Errorf("TmdbID = %v, want *1396", show.TmdbID)
	}
	if show.Runtime == nil || *show.Runtime != 47 {
		t.Errorf("Runtime = %v, want *47", show.Runtime)
	}
	if got := show.MalIDs; len(got) != 2 || got[0] != 9876 || got[1] != 9877 {
		t.Errorf("MalIDs = %v, want [9876 9877]", got)
	}
	if got := show.AniListIDs; len(got) != 1 || got[0] != 55555 {
		t.Errorf("AniListIDs = %v, want [55555]", got)
	}
	if show.TimeOfDay == nil || show.TimeOfDay.Hours != 22 || show.TimeOfDay.Minutes != 0 {
		t.Errorf("TimeOfDay = %+v, want *{22, 0}", show.TimeOfDay)
	}
	if show.Rating == nil || show.Rating.Count != 12345 || show.Rating.Value != 9.4 {
		t.Errorf("Rating = %+v, want *{12345, 9.4}", show.Rating)
	}
	if got := show.Genres; len(got) != 2 || got[0] != "Drama" || got[1] != "Thriller" {
		t.Errorf("Genres = %v, want [Drama Thriller]", got)
	}
	if got := show.Actors; len(got) != 1 || got[0].Name != "Bryan Cranston" {
		t.Errorf("Actors[0].Name = %q, want Bryan Cranston", got[0].Name)
	}
	if got := show.Images; len(got) != 2 || got[0].CoverType != "poster" || got[1].CoverType != "fanart" {
		t.Errorf("Images coverTypes = %+v, want [poster fanart]", got)
	}
	if got := show.Seasons; len(got) != 2 || got[0].SeasonNumber != 0 || got[1].SeasonNumber != 1 {
		t.Errorf("Seasons = %+v, want [0,1]", got)
	}
	if got := show.Episodes; len(got) != 1 {
		t.Fatalf("Episodes length = %d, want 1", len(got))
	}
	ep := show.Episodes[0]
	if ep.TvdbID != 349231 || ep.SeasonNumber != 1 || ep.EpisodeNumber != 1 {
		t.Errorf("Episode ids = (%d, %d, %d), want (349231, 1, 1)", ep.TvdbID, ep.SeasonNumber, ep.EpisodeNumber)
	}
	if ep.AbsoluteEpisodeNumber == nil || *ep.AbsoluteEpisodeNumber != 1 {
		t.Errorf("AbsoluteEpisodeNumber = %v, want *1", ep.AbsoluteEpisodeNumber)
	}
	if ep.AiredAfterSeasonNumber != nil {
		t.Errorf("AiredAfterSeasonNumber = %v, want nil", *ep.AiredAfterSeasonNumber)
	}
	if ep.AirDate != "2008-01-20" {
		t.Errorf("AirDate = %q, want 2008-01-20", ep.AirDate)
	}
	if ep.AirDateUtc == nil {
		t.Fatalf("AirDateUtc = nil, want non-nil")
	}
	want := time.Date(2008, 1, 21, 3, 0, 0, 0, time.UTC)
	if !ep.AirDateUtc.Equal(want) {
		t.Errorf("AirDateUtc = %v, want %v", ep.AirDateUtc, want)
	}
	if ep.Runtime != 58 {
		t.Errorf("Runtime = %d, want 58", ep.Runtime)
	}
	if ep.FinaleType != "" {
		t.Errorf("FinaleType = %q, want empty (null in JSON)", ep.FinaleType)
	}
	if ep.Rating == nil || ep.Rating.Count != 100 || ep.Rating.Value != 8.7 {
		t.Errorf("Rating = %+v, want *{100, 8.7}", ep.Rating)
	}
}

// TestShowResource_NullSubObjects exercises the *RatingResource +
// *TimeOfDayResource nullability — SkyHook legitimately omits both
// for shows without ratings or air-time, and the decode must produce
// a nil pointer rather than a zero-valued sub-object the MapSeries
// pass would mistake for "actually rated zero".
func TestShowResource_NullSubObjects(t *testing.T) {
	payload := []byte(`{
		"tvdbId": 1,
		"title": "No-rating show",
		"timeOfDay": null,
		"rating": null
	}`)
	var show ShowResource
	if err := json.Unmarshal(payload, &show); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if show.TimeOfDay != nil {
		t.Errorf("TimeOfDay = %+v, want nil", show.TimeOfDay)
	}
	if show.Rating != nil {
		t.Errorf("Rating = %+v, want nil", show.Rating)
	}
}

// TestEpisodeResource_AirDateUtcNull exercises the *time.Time
// nullability — episodes without an aired-date round-trip to
// AirDateUtc == nil, which the MapEpisode pass treats as "no
// episodes.Episode.AirDateUtc value" (the Go DB column accepts
// NULL).
func TestEpisodeResource_AirDateUtcNull(t *testing.T) {
	payload := []byte(`{
		"tvdbId": 999,
		"seasonNumber": 1,
		"episodeNumber": 1,
		"title": "Unaired",
		"airDate": "",
		"airDateUtc": null,
		"runtime": 0
	}`)
	var ep EpisodeResource
	if err := json.Unmarshal(payload, &ep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ep.AirDateUtc != nil {
		t.Errorf("AirDateUtc = %v, want nil", ep.AirDateUtc)
	}
	if ep.AirDate != "" {
		t.Errorf("AirDate = %q, want empty", ep.AirDate)
	}
}

// TestShowResource_OmittedNullables exercises the case where SkyHook
// omits the nullable fields entirely (older responses didn't always
// include tvMazeId / tmdbId / runtime). The decoder must leave the
// pointers nil rather than zero them.
func TestShowResource_OmittedNullables(t *testing.T) {
	payload := []byte(`{
		"tvdbId": 2,
		"title": "Minimal show"
	}`)
	var show ShowResource
	if err := json.Unmarshal(payload, &show); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if show.TvRageID != nil {
		t.Errorf("TvRageID = %v, want nil", show.TvRageID)
	}
	if show.TvMazeID != nil {
		t.Errorf("TvMazeID = %v, want nil", show.TvMazeID)
	}
	if show.TmdbID != nil {
		t.Errorf("TmdbID = %v, want nil", show.TmdbID)
	}
	if show.Runtime != nil {
		t.Errorf("Runtime = %v, want nil", show.Runtime)
	}
}
