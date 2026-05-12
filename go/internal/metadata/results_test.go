package metadata_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/metadata"
)

func TestNewMetadataFileResult(t *testing.T) {
	t.Parallel()
	r := metadata.NewMetadataFileResult("tvshow.nfo", "<tvshow/>")
	if r.RelativePath != "tvshow.nfo" {
		t.Errorf("RelativePath = %q", r.RelativePath)
	}
	if r.Contents != "<tvshow/>" {
		t.Errorf("Contents = %q", r.Contents)
	}
}

func TestNewImageFileResult(t *testing.T) {
	t.Parallel()
	r := metadata.NewImageFileResult("poster.jpg", "https://cdn.example/poster.jpg")
	if r.RelativePath != "poster.jpg" {
		t.Errorf("RelativePath = %q", r.RelativePath)
	}
	if r.URL != "https://cdn.example/poster.jpg" {
		t.Errorf("URL = %q", r.URL)
	}
}

func TestMetadataFileResult_ZeroValue(t *testing.T) {
	t.Parallel()
	var r metadata.MetadataFileResult
	if r.RelativePath != "" || r.Contents != "" {
		t.Errorf("zero value not empty: %+v", r)
	}
}

func TestImageFileResult_ZeroValue(t *testing.T) {
	t.Parallel()
	var r metadata.ImageFileResult
	if r.RelativePath != "" || r.URL != "" {
		t.Errorf("zero value not empty: %+v", r)
	}
}
