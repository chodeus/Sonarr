package autotagging

import (
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/rootfolders"
	"sonarr.tv/sonarr/internal/series"
)

func TestChangesFor_EmptyAutoTagsReturnsEmpty(t *testing.T) {
	s := &series.Series{Path: filepath.Join("/tv", "Series.X")}
	got := ChangesFor(s, nil, []rootfolders.Folder{{Path: "/tv"}})
	if len(got.TagsToAdd) != 0 || len(got.TagsToRemove) != 0 {
		t.Errorf("empty autoTags should yield empty changes; got %+v", got)
	}
}

func TestChangesFor_AddsMatchedTags(t *testing.T) {
	autoTag := &Tag{
		Name:           "anime",
		Tags:           []int{10, 20},
		Specifications: []Specification{&MonitoredSpec{}},
	}
	s := &series.Series{Monitored: true, Tags: []int{10}, Path: filepath.Join("/tv", "Series")}
	got := ChangesFor(s, []*Tag{autoTag}, nil)

	// Tag 10 already on series → not added; tag 20 → added.
	if _, has := got.TagsToAdd[20]; !has {
		t.Errorf("expected tag 20 in TagsToAdd: %+v", got)
	}
	if _, has := got.TagsToAdd[10]; has {
		t.Errorf("tag 10 already on series; should NOT be in TagsToAdd: %+v", got)
	}
	if len(got.TagsToRemove) != 0 {
		t.Errorf("non-matching path should not populate TagsToRemove: %+v", got)
	}
}

func TestChangesFor_DoesNotRemoveWhenFlagOff(t *testing.T) {
	autoTag := &Tag{
		Name:                    "anime",
		Tags:                    []int{10, 20},
		RemoveTagsAutomatically: false,
		Specifications:          []Specification{&MonitoredSpec{}},
	}
	s := &series.Series{Monitored: false, Tags: []int{10}, Path: filepath.Join("/tv", "Series")}
	got := ChangesFor(s, []*Tag{autoTag}, nil)

	// Spec doesn't match (Monitored=false) AND
	// RemoveTagsAutomatically=false → no changes.
	if len(got.TagsToAdd) != 0 || len(got.TagsToRemove) != 0 {
		t.Errorf("RemoveTagsAutomatically=false should produce empty changes; got %+v", got)
	}
}

func TestChangesFor_RemovesTagsWhenFlagOn(t *testing.T) {
	autoTag := &Tag{
		Name:                    "anime",
		Tags:                    []int{10, 20},
		RemoveTagsAutomatically: true,
		Specifications:          []Specification{&MonitoredSpec{}},
	}
	s := &series.Series{Monitored: false, Tags: []int{10}, Path: filepath.Join("/tv", "Series")}
	got := ChangesFor(s, []*Tag{autoTag}, nil)

	// Spec doesn't match, RemoveTagsAutomatically=true → all tags
	// in autoTag.Tags scheduled for removal.
	if _, has := got.TagsToRemove[10]; !has {
		t.Errorf("expected tag 10 in TagsToRemove: %+v", got)
	}
	if _, has := got.TagsToRemove[20]; !has {
		t.Errorf("expected tag 20 in TagsToRemove: %+v", got)
	}
	if len(got.TagsToAdd) != 0 {
		t.Errorf("non-matching path should not populate TagsToAdd: %+v", got)
	}
}

func TestChangesFor_RootFolderPathPlumbing(t *testing.T) {
	autoTag := &Tag{
		Name: "by-root",
		Tags: []int{99},
		Specifications: []Specification{
			&RootFolderSpec{Value: filepath.Join("/tv", "anime")},
		},
	}
	folders := []rootfolders.Folder{
		{ID: 1, Path: "/tv"},
		{ID: 2, Path: filepath.Join("/tv", "anime")},
	}

	t.Run("series under /tv/anime gets the deeper root → match", func(t *testing.T) {
		s := &series.Series{Path: filepath.Join("/tv", "anime", "X")}
		got := ChangesFor(s, []*Tag{autoTag}, folders)
		if _, has := got.TagsToAdd[99]; !has {
			t.Errorf("expected tag 99 to be added (series under /tv/anime): %+v", got)
		}
	})

	t.Run("series under /tv directly does NOT match anime root", func(t *testing.T) {
		s := &series.Series{Path: filepath.Join("/tv", "Y")}
		got := ChangesFor(s, []*Tag{autoTag}, folders)
		if _, has := got.TagsToAdd[99]; has {
			t.Errorf("did not expect tag 99 (series under /tv, not /tv/anime): %+v", got)
		}
	})
}

func TestChangesFor_NilSeriesReturnsEmpty(t *testing.T) {
	autoTag := &Tag{Tags: []int{1}, Specifications: []Specification{&MonitoredSpec{}}}
	got := ChangesFor(nil, []*Tag{autoTag}, nil)
	if len(got.TagsToAdd) != 0 || len(got.TagsToRemove) != 0 {
		t.Errorf("nil series should yield empty changes; got %+v", got)
	}
}

func TestChangesFor_EmptySpecsAlwaysMatches(t *testing.T) {
	// C# vacuous-truth: an AutoTag with no specs yields
	// `All(false) → true` so the AutoTag's tags are always added.
	autoTag := &Tag{Name: "no-specs", Tags: []int{42}, Specifications: nil}
	s := &series.Series{Path: filepath.Join("/tv", "X")}
	got := ChangesFor(s, []*Tag{autoTag}, nil)
	if _, has := got.TagsToAdd[42]; !has {
		t.Errorf("AutoTag with no specs should always match; got %+v", got)
	}
}

func TestChangesFor_DedupesAcrossAutoTags(t *testing.T) {
	// Two AutoTags both schedule tag 5 for adding → final set has
	// just one entry.
	at1 := &Tag{Name: "a", Tags: []int{5}, Specifications: []Specification{&MonitoredSpec{}}}
	at2 := &Tag{Name: "b", Tags: []int{5}, Specifications: []Specification{&MonitoredSpec{}}}
	s := &series.Series{Monitored: true, Path: filepath.Join("/tv", "X")}
	got := ChangesFor(s, []*Tag{at1, at2}, nil)
	if len(got.TagsToAdd) != 1 {
		t.Errorf("expected 1 unique tag, got %d: %+v", len(got.TagsToAdd), got)
	}
}
