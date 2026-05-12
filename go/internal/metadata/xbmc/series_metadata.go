package xbmc

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/series"
)

// SeriesMetadata builds the `tvshow.nfo` body for the given series.
// Ports C# `XbmcMetadata.SeriesMetadata` at
// `src/NzbDrone.Core/Extras/Metadata/Consumers/Xbmc/XbmcMetadata.cs:141-276`.
//
// Two independent setting gates determine emission:
//
//   - [Settings.SeriesMetadata]: when true, render the full XML body
//     (declaration + `<tvshow>` root with title, rating, plot, mpaa,
//     id, uniqueid×N, genre×N, tag×N, status, premiered, enddate,
//     studio, actor×N, episodeguide).
//   - [Settings.SeriesMetadataUrl]: when true, append the TVDB
//     browser URL after the XML body (separated by a newline if both
//     gates are on, alone otherwise).
//
// `reason == [metadata.SeriesMetadataReasonEpisodesImported]` short-
// circuits to a zero-value result — matches C# `:143-146` "post-import
// regenerations don't rebuild series metadata to avoid I/O thrash on
// per-episode imports".
//
// Returns zero-value [metadata.MetadataFileResult] (RelativePath=="")
// when nothing would be emitted. The consumer-facing service treats
// that as "writer chose to skip" — same C# semantics as a null return.
//
// Goroutine-safe: writes to a per-call [bytes.Buffer] + uses
// encoding/xml.Encoder which holds no global state. Concurrent
// SeriesMetadata calls on the same XbmcMetadata instance are safe.
//
// XML format divergence from C# `XDocument.Save`:
//   - The C# tree's `XmlWriterSettings { Indent = true }` defaults
//     to 2-space indents. Go's encoding/xml.Encoder.Indent("", "  ")
//     produces the same shape — verified via the W2c-i tests.
//   - The XML declaration is `<?xml version="1.0" encoding="UTF-8"
//     standalone="yes"?>` matching the C# `XDeclaration("1.0",
//     "UTF-8", "yes")` byte-for-byte.
//   - Sub-element ordering matches the C# `XElement` add-sequence;
//     Kodi parses tolerantly so order isn't load-bearing, but the
//     port mirrors C# anyway for git-diff readability of the
//     contract corpus.
//   - The `<rating>` element value is the C# decimal formatted by
//     .NET as e.g. "7.85"; the Go port uses `strconv.FormatFloat(...,
//     'f', -1, 64)` which produces equivalent text for the values
//     SkyHook returns. Edge cases (extreme decimals, trailing zeros)
//     are documented divergences acceptable for the .nfo target.
func (m *XbmcMetadata) SeriesMetadata(ctx context.Context, s *series.Series, reason metadata.SeriesMetadataReason) (metadata.MetadataFileResult, error) {
	if m == nil || s == nil {
		return metadata.MetadataFileResult{}, nil
	}
	if reason == metadata.SeriesMetadataReasonEpisodesImported {
		return metadata.MetadataFileResult{}, nil
	}

	var body strings.Builder

	if m.settings.SeriesMetadata {
		xmlBody, err := m.buildSeriesXML(ctx, s)
		if err != nil {
			return metadata.MetadataFileResult{}, err
		}
		body.WriteString(xmlBody)
	}

	if m.settings.SeriesMetadataUrl && s.TvdbID > 0 {
		if body.Len() > 0 {
			body.WriteString("\n")
		}
		body.WriteString("https://www.thetvdb.com/?tab=series&id=")
		body.WriteString(strconv.Itoa(s.TvdbID))
	}

	if strings.TrimSpace(body.String()) == "" {
		return metadata.MetadataFileResult{}, nil
	}
	return metadata.NewMetadataFileResult("tvshow.nfo", body.String()), nil
}

// buildSeriesXML builds the `<tvshow>` element body — everything
// inside the C# `Settings.SeriesMetadata` gate at `XbmcMetadata.cs:150-263`.
//
// Decode failures on series.Ratings / series.Actors / series.Genres
// produce wrapped errors; the caller surfaces them.
//
// Tag-lookup errors are NON-fatal: log + drop the tag block but keep
// the rest of the XML body. The C# tree at `:198-206` doesn't have a
// fallible tag lookup (the in-process repo can't error), so the Go
// port absorbs the I/O-error class to match the spirit of "writer
// emits as much as it can".
func (m *XbmcMetadata) buildSeriesXML(ctx context.Context, s *series.Series) (string, error) {
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

	var root struct {
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

	root.Title = s.Title

	// `<rating>` is gated on Ratings.Votes > 0 per C# `:158-161`.
	ratings, err := decodeRatings(s.Ratings)
	if err != nil {
		return "", fmt.Errorf("xbmc: SeriesMetadata: decode series.Ratings: %w", err)
	}
	if ratings.Votes > 0 {
		formatted := strconv.FormatFloat(ratings.Value, 'f', -1, 64)
		root.Rating = &formatted
	}

	root.Plot = s.Overview
	root.Mpaa = s.Certification
	root.ID = s.TvdbID

	// `<uniqueid type="tvdb" default="true">` — always emitted per
	// C# `:166-170`.
	root.UniqueIDs = append(root.UniqueIDs, uniqueIDXML{
		Type:    "tvdb",
		Default: "true",
		Value:   strconv.Itoa(s.TvdbID),
	})
	if strings.TrimSpace(s.ImdbID) != "" {
		root.UniqueIDs = append(root.UniqueIDs, uniqueIDXML{
			Type:  "imdb",
			Value: s.ImdbID,
		})
	}
	if s.TmdbID > 0 {
		root.UniqueIDs = append(root.UniqueIDs, uniqueIDXML{
			Type:  "tmdb",
			Value: strconv.Itoa(s.TmdbID),
		})
	}
	if s.TvMazeID > 0 {
		root.UniqueIDs = append(root.UniqueIDs, uniqueIDXML{
			Type:  "tvmaze",
			Value: strconv.Itoa(s.TvMazeID),
		})
	}

	genres, err := decodeGenres(s.Genres)
	if err != nil {
		return "", fmt.Errorf("xbmc: SeriesMetadata: decode series.Genres: %w", err)
	}
	root.Genres = genres

	if len(s.Tags) > 0 && m.tags != nil {
		tagRows, tagErr := m.tags.GetByIDs(ctx, s.Tags)
		if tagErr == nil {
			root.Tags = make([]string, 0, len(tagRows))
			for _, t := range tagRows {
				root.Tags = append(root.Tags, t.Label)
			}
		}
		// Tag-lookup error is non-fatal — see method doc.
	}

	root.Status = seriesStatusName(s.Status)

	if s.FirstAired != nil {
		root.Premiered = s.FirstAired.Format("2006-01-02")
	}
	// `<enddate>` only when series is ended AND has LastAired set —
	// C# `:215-219`. Jellyfin-specific tag.
	if s.Status == series.SeriesStatusEnded && s.LastAired != nil {
		root.EndDate = s.LastAired.Format("2006-01-02")
	}

	root.Studio = s.Network

	actors, err := decodeActors(s.Actors)
	if err != nil {
		return "", fmt.Errorf("xbmc: SeriesMetadata: decode series.Actors: %w", err)
	}
	for _, a := range actors {
		ax := actorXML{Name: a.Name, Role: a.Character}
		if len(a.Images) > 0 {
			ax.Thumb = a.Images[0].RemoteUrl
		}
		root.Actors = append(root.Actors, ax)
	}

	if m.settings.SeriesMetadataEpisodeGuide {
		guide := NewKodiEpisodeGuide(s)
		guideJSON, jErr := json.Marshal(&guide)
		if jErr != nil {
			return "", fmt.Errorf("xbmc: SeriesMetadata: marshal episodeguide: %w", jErr)
		}
		root.EpisodeGuide = string(guideJSON)
	}

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(&root); err != nil {
		return "", fmt.Errorf("xbmc: SeriesMetadata: xml encode: %w", err)
	}
	return buf.String(), nil
}

// ratingsBlob is the [series.Series.Ratings] JSON-blob shape.
// Mirrors C# `Tv.Ratings` at `src/NzbDrone.Core/Tv/Ratings.cs:5-8`:
// {votes int, value decimal}. JSON keys are lowerCamelCase matching
// the persisted blob.
type ratingsBlob struct {
	Votes int     `json:"votes"`
	Value float64 `json:"value"`
}

// decodeRatings parses the [series.Series.Ratings] JSON blob. Empty
// / blank input → zero-value ratings (Votes == 0 disables the
// `<rating>` element emission via the C# `:158` gate).
func decodeRatings(blob string) (ratingsBlob, error) {
	trimmed := strings.TrimSpace(blob)
	if trimmed == "" || trimmed == "{}" {
		return ratingsBlob{}, nil
	}
	var r ratingsBlob
	if err := json.Unmarshal([]byte(trimmed), &r); err != nil {
		return ratingsBlob{}, err
	}
	return r, nil
}

// decodeGenres parses the [series.Series.Genres] JSON blob — a flat
// string array. Empty / blank input → nil.
func decodeGenres(blob string) ([]string, error) {
	trimmed := strings.TrimSpace(blob)
	if trimmed == "" || trimmed == "[]" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// actorBlob is the [series.Series.Actors] JSON-blob entry shape.
// Mirrors C# `Tv.Actor` at `src/NzbDrone.Core/Tv/Actor.cs:5-15`:
// {name, character, images[]}.
type actorBlob struct {
	Name      string             `json:"name"`
	Character string             `json:"character"`
	Images    []mediacover.Cover `json:"images"`
}

// decodeActors parses the [series.Series.Actors] JSON blob. Empty
// / blank input → nil.
func decodeActors(blob string) ([]actorBlob, error) {
	trimmed := strings.TrimSpace(blob)
	if trimmed == "" || trimmed == "[]" {
		return nil, nil
	}
	var out []actorBlob
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// seriesStatusName maps a [series.SeriesStatus*] int to the C# enum
// name string. Mirrors `.NET Enum.ToString()` for the
// [SeriesStatusType] enum — when XElement embeds an enum, .NET emits
// the name.
//
// Unknown / negative values produce an empty string — the C# tree
// would emit the integer text for an unknown enum value; the Go port
// chooses empty to make Kodi parse failures obvious rather than
// silently produce a non-Kodi-canonical status word.
func seriesStatusName(status int) string {
	switch status {
	case series.SeriesStatusContinuing:
		return "Continuing"
	case series.SeriesStatusEnded:
		return "Ended"
	case series.SeriesStatusUpcoming:
		return "Upcoming"
	case series.SeriesStatusDeleted:
		return "Deleted"
	default:
		return ""
	}
}
