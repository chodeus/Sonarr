package metadata_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/metadata"
)

func TestMetadataType_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   metadata.MetadataType
		want string
	}{
		{metadata.MetadataTypeUnknown, "Unknown"},
		{metadata.MetadataTypeSeriesMetadata, "SeriesMetadata"},
		{metadata.MetadataTypeEpisodeMetadata, "EpisodeMetadata"},
		{metadata.MetadataTypeSeriesImage, "SeriesImage"},
		{metadata.MetadataTypeSeasonImage, "SeasonImage"},
		{metadata.MetadataTypeEpisodeImage, "EpisodeImage"},
		{metadata.MetadataType(99), "MetadataType(99)"},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("MetadataType(%d).String() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMetadataType_Ordinals(t *testing.T) {
	t.Parallel()
	// These ordinals are persisted in MetadataFiles.Type and surface
	// in the v3 API. A renumbering would silently corrupt existing
	// rows. Pin the wire values.
	cases := map[metadata.MetadataType]int{
		metadata.MetadataTypeUnknown:         0,
		metadata.MetadataTypeSeriesMetadata:  1,
		metadata.MetadataTypeEpisodeMetadata: 2,
		metadata.MetadataTypeSeriesImage:     3,
		metadata.MetadataTypeSeasonImage:     4,
		metadata.MetadataTypeEpisodeImage:    5,
	}
	for k, want := range cases {
		if int(k) != want {
			t.Errorf("%s ordinal = %d, want %d", k, int(k), want)
		}
	}
}

func TestMetadataType_Section(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   metadata.MetadataType
		want string
	}{
		{metadata.MetadataTypeSeriesMetadata, metadata.SectionMetadata},
		{metadata.MetadataTypeEpisodeMetadata, metadata.SectionMetadata},
		{metadata.MetadataTypeSeriesImage, metadata.SectionImage},
		{metadata.MetadataTypeSeasonImage, metadata.SectionImage},
		{metadata.MetadataTypeEpisodeImage, metadata.SectionImage},
		{metadata.MetadataTypeUnknown, ""},
		{metadata.MetadataType(99), ""},
	}
	for _, tc := range cases {
		if got := tc.in.Section(); got != tc.want {
			t.Errorf("%s.Section() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMetadataType_SectionConstants(t *testing.T) {
	t.Parallel()
	if metadata.SectionMetadata != "metadata" {
		t.Errorf("SectionMetadata = %q, want %q", metadata.SectionMetadata, "metadata")
	}
	if metadata.SectionImage != "image" {
		t.Errorf("SectionImage = %q, want %q", metadata.SectionImage, "image")
	}
}
