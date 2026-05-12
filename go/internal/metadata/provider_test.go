package metadata_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
)

func TestSeriesMetadataReason_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   metadata.SeriesMetadataReason
		want string
	}{
		{metadata.SeriesMetadataReasonScan, "Scan"},
		{metadata.SeriesMetadataReasonEpisodeFolderCreated, "EpisodeFolderCreated"},
		{metadata.SeriesMetadataReasonEpisodesImported, "EpisodesImported"},
		{metadata.SeriesMetadataReason(42), "SeriesMetadataReason(42)"},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("%d.String() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSeriesMetadataReason_Ordinals(t *testing.T) {
	t.Parallel()
	cases := map[metadata.SeriesMetadataReason]int{
		metadata.SeriesMetadataReasonScan:                 0,
		metadata.SeriesMetadataReasonEpisodeFolderCreated: 1,
		metadata.SeriesMetadataReasonEpisodesImported:     2,
	}
	for k, want := range cases {
		if int(k) != want {
			t.Errorf("%s ordinal = %d, want %d", k, int(k), want)
		}
	}
}

func TestSeason_ZeroValue(t *testing.T) {
	t.Parallel()
	var s metadata.Season
	if s.SeasonNumber != 0 || s.Monitored {
		t.Errorf("zero value not empty: %+v", s)
	}
}

func TestTestFailure_ZeroValue(t *testing.T) {
	t.Parallel()
	var f metadata.TestFailure
	if f.PropertyName != "" || f.ErrorMessage != "" {
		t.Errorf("zero value not empty: %+v", f)
	}
}

// fakeProvider exercises the [metadata.Provider] interface to confirm
// the shape compiles and the zero-value convention works. Mirrors the
// notifications + importlists package's interface-shape tests.
type fakeProvider struct {
	name       string
	definition *metadata.Metadata
	failTest   bool
}

func (p *fakeProvider) Name() string                   { return p.name }
func (p *fakeProvider) Definition() *metadata.Metadata { return p.definition }

func (p *fakeProvider) GetFilenameAfterMove(_ *series.Series, _ *episodefiles.File, mf *metadatafiles.MetadataFile) string {
	if mf == nil {
		return ""
	}
	return mf.RelativePath
}

func (p *fakeProvider) FindMetadataFile(_ *series.Series, _ string) (*metadatafiles.MetadataFile, error) {
	return nil, nil
}

func (p *fakeProvider) SeriesMetadata(_ context.Context, _ *series.Series, _ metadata.SeriesMetadataReason) (metadata.MetadataFileResult, error) {
	return metadata.MetadataFileResult{}, nil
}

func (p *fakeProvider) EpisodeMetadata(_ context.Context, _ *series.Series, _ *episodefiles.File) (metadata.MetadataFileResult, error) {
	return metadata.MetadataFileResult{}, nil
}

func (p *fakeProvider) SeriesImages(_ context.Context, _ *series.Series) ([]metadata.ImageFileResult, error) {
	return nil, nil
}

func (p *fakeProvider) SeasonImages(_ context.Context, _ *series.Series, _ metadata.Season) ([]metadata.ImageFileResult, error) {
	return nil, nil
}

func (p *fakeProvider) EpisodeImages(_ context.Context, _ *series.Series, _ *episodefiles.File) ([]metadata.ImageFileResult, error) {
	return nil, nil
}

func (p *fakeProvider) Test(_ context.Context) ([]metadata.TestFailure, error) {
	if p.failTest {
		return nil, errors.New("boom")
	}
	return nil, nil
}

func (p *fakeProvider) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

func TestProvider_FakeImplementation(t *testing.T) {
	t.Parallel()
	var p metadata.Provider = &fakeProvider{
		name:       "Kodi",
		definition: &metadata.Metadata{ID: 7, Name: "Kodi", Implementation: "XbmcMetadata"},
	}
	if got := p.Name(); got != "Kodi" {
		t.Errorf("Name() = %q", got)
	}
	if d := p.Definition(); d == nil || d.ID != 7 {
		t.Errorf("Definition() = %+v", d)
	}

	res, err := p.SeriesMetadata(t.Context(), &series.Series{ID: 1}, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("SeriesMetadata: %v", err)
	}
	if res.RelativePath != "" || res.Contents != "" {
		t.Errorf("zero-skip convention broke: %+v", res)
	}

	mf := &metadatafiles.MetadataFile{RelativePath: "tvshow.nfo"}
	if got := p.GetFilenameAfterMove(&series.Series{}, &episodefiles.File{}, mf); got != "tvshow.nfo" {
		t.Errorf("GetFilenameAfterMove() = %q", got)
	}

	failures, err := p.Test(t.Context())
	if err != nil {
		t.Errorf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test failures unexpected: %+v", failures)
	}

	bad := &fakeProvider{failTest: true}
	if _, err := bad.Test(t.Context()); err == nil {
		t.Errorf("expected error from failTest")
	}
}
