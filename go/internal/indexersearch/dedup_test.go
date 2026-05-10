package indexersearch

import (
	"testing"

	"sonarr.tv/sonarr/internal/parser"
)

func TestDeDupReleases_Empty(t *testing.T) {
	if got := DeDupReleases(nil); got != nil {
		t.Errorf("nil → nil expected")
	}
	if got := DeDupReleases([]any{}); got != nil {
		t.Errorf("empty → nil expected")
	}
}

func TestDeDupReleases_NoDuplicates(t *testing.T) {
	a := &parser.ReleaseInfo{Guid: "g1", Title: "a"}
	b := &parser.ReleaseInfo{Guid: "g2", Title: "b"}
	got := DeDupReleases([]any{a, b})
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0] != any(a) || got[1] != any(b) {
		t.Errorf("order not preserved")
	}
}

func TestDeDupReleases_LowestPriorityWins(t *testing.T) {
	a := &parser.ReleaseInfo{Guid: "g1", Title: "a", IndexerPriority: 30}
	b := &parser.ReleaseInfo{Guid: "g1", Title: "b", IndexerPriority: 10}
	c := &parser.ReleaseInfo{Guid: "g1", Title: "c", IndexerPriority: 20}
	got := DeDupReleases([]any{a, b, c})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	r := got[0].(*parser.ReleaseInfo)
	if r.Title != "b" {
		t.Errorf("expected lowest-priority survivor 'b', got %q", r.Title)
	}
}

func TestDeDupReleases_TieKeepsFirstSeen(t *testing.T) {
	a := &parser.ReleaseInfo{Guid: "g1", Title: "first", IndexerPriority: 25}
	b := &parser.ReleaseInfo{Guid: "g1", Title: "second", IndexerPriority: 25}
	got := DeDupReleases([]any{a, b})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	r := got[0].(*parser.ReleaseInfo)
	if r.Title != "first" {
		t.Errorf("tie should keep first-seen, got %q", r.Title)
	}
}

func TestDeDupReleases_TorrentInfoUpcast(t *testing.T) {
	a := &parser.TorrentInfo{ReleaseInfo: parser.ReleaseInfo{Guid: "g1", Title: "torrentA", IndexerPriority: 5}}
	b := &parser.ReleaseInfo{Guid: "g1", Title: "ngA", IndexerPriority: 10}
	got := DeDupReleases([]any{b, a}) // bare ReleaseInfo first, TorrentInfo second
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	t.Logf("survivor: %T", got[0])
	if r, ok := got[0].(*parser.TorrentInfo); !ok || r.Title != "torrentA" {
		t.Errorf("expected *parser.TorrentInfo with title 'torrentA', got %T (%+v)", got[0], got[0])
	}
}

func TestDeDupReleases_EmptyGuidPassesThrough(t *testing.T) {
	a := &parser.ReleaseInfo{Guid: "", Title: "no-guid-1"}
	b := &parser.ReleaseInfo{Guid: "", Title: "no-guid-2"}
	c := &parser.ReleaseInfo{Guid: "g1", Title: "deduped"}
	got := DeDupReleases([]any{a, b, c})
	if len(got) != 3 {
		t.Errorf("expected 3 (empty Guid is not deduped), got %d", len(got))
	}
}

func TestDeDupReleases_UnknownShapesDropped(t *testing.T) {
	a := &parser.ReleaseInfo{Guid: "g1", Title: "ok"}
	got := DeDupReleases([]any{a, "not-a-release", 42, nil})
	if len(got) != 1 {
		t.Errorf("expected 1 (drop unknown shapes), got %d", len(got))
	}
}

func TestDeDupReleases_MixedShapesPreserveOrder(t *testing.T) {
	r1 := &parser.ReleaseInfo{Guid: "a", IndexerPriority: 25}
	r2 := &parser.TorrentInfo{ReleaseInfo: parser.ReleaseInfo{Guid: "b", IndexerPriority: 25}}
	r3 := &parser.ReleaseInfo{Guid: "c", IndexerPriority: 25}
	got := DeDupReleases([]any{r1, r2, r3})
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	if got[0] != any(r1) || got[1] != any(r2) || got[2] != any(r3) {
		t.Errorf("order not preserved across mixed shapes")
	}
}
