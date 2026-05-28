package queue_test

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/queue"
)

func ids(rows []queue.Queue) []int {
	out := make([]int, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func eq(t *testing.T, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("order: got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("order: got %v want %v", got, want)
		}
	}
}

func TestSortQueue_Title(t *testing.T) {
	t.Parallel()
	rows := []queue.Queue{
		{ID: 1, Title: "Charlie"},
		{ID: 2, Title: "alpha"},
		{ID: 3, Title: "Bravo"},
	}
	// Ascending by title (case-sensitive ordinal, matching C# string sort).
	asc := queue.SortQueue(rows, "title", true, nil)
	eq(t, ids(asc), []int{3, 1, 2}) // "Bravo" < "Charlie" < "alpha" (uppercase before lowercase)
	desc := queue.SortQueue(rows, "title", false, nil)
	eq(t, ids(desc), []int{2, 1, 3})
}

func TestSortQueue_Size(t *testing.T) {
	t.Parallel()
	rows := []queue.Queue{
		{ID: 1, Size: 300},
		{ID: 2, Size: 100},
		{ID: 3, Size: 200},
	}
	eq(t, ids(queue.SortQueue(rows, "size", true, nil)), []int{2, 3, 1})
	eq(t, ids(queue.SortQueue(rows, "size", false, nil)), []int{1, 3, 2})
}

func TestSortQueue_TimeLeftNilsLast(t *testing.T) {
	t.Parallel()
	d1 := 1 * time.Hour
	d2 := 2 * time.Hour
	rows := []queue.Queue{
		{ID: 1, TimeLeft: nil},
		{ID: 2, TimeLeft: &d2},
		{ID: 3, TimeLeft: &d1},
	}
	// Ascending: nil (no ETA) sorts LAST.
	eq(t, ids(queue.SortQueue(rows, "timeleft", true, nil)), []int{3, 2, 1})
}

func TestSortQueue_Added(t *testing.T) {
	t.Parallel()
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	rows := []queue.Queue{
		{ID: 1, Added: &t2},
		{ID: 2, Added: nil},
		{ID: 3, Added: &t1},
	}
	// Ascending: oldest first, nil last.
	eq(t, ids(queue.SortQueue(rows, "added", true, nil)), []int{3, 1, 2})
}

func TestSortQueue_ProgressSecondaryTiebreak(t *testing.T) {
	t.Parallel()
	// Same title → tie on primary; secondary is progress DESC
	// (most-complete first), regardless of primary direction.
	rows := []queue.Queue{
		{ID: 1, Title: "Show", Size: 100, SizeLeft: 90}, // 10% done
		{ID: 2, Title: "Show", Size: 100, SizeLeft: 10}, // 90% done
		{ID: 3, Title: "Show", Size: 100, SizeLeft: 50}, // 50% done
	}
	asc := queue.SortQueue(rows, "title", true, nil)
	eq(t, ids(asc), []int{2, 3, 1}) // 90% → 50% → 10%
	// Descending primary, but secondary still progress DESC.
	desc := queue.SortQueue(rows, "title", false, nil)
	eq(t, ids(desc), []int{2, 3, 1})
}

func TestSortQueue_QualityWeight(t *testing.T) {
	t.Parallel()
	rows := []queue.Queue{
		{ID: 1, Quality: quality.Model{Quality: quality.Quality{ID: 7}}},
		{ID: 2, Quality: quality.Model{Quality: quality.Quality{ID: 9}}},
		{ID: 3, Quality: quality.Model{Quality: quality.Quality{ID: 4}}},
	}
	// Weight map: id 4 -> 1, id 7 -> 2, id 9 -> 3 (id 9 is highest quality).
	weight := func(q quality.Quality) int {
		return map[int]int{4: 1, 7: 2, 9: 3}[q.ID]
	}
	eq(t, ids(queue.SortQueue(rows, "quality", true, weight)), []int{3, 1, 2})
	eq(t, ids(queue.SortQueue(rows, "quality", false, weight)), []int{2, 1, 3})
}

func TestSortQueue_Languages(t *testing.T) {
	t.Parallel()
	en := language.Language{ID: 1, Name: "English"}
	fr := language.Language{ID: 2, Name: "French"}
	rows := []queue.Queue{
		{ID: 1, Languages: nil},                         // empty → last
		{ID: 2, Languages: []language.Language{fr}},     // single "French"
		{ID: 3, Languages: []language.Language{en}},     // single "English"
		{ID: 4, Languages: []language.Language{en, fr}}, // multi → after singles
	}
	// Ascending: single English < single French < multi < empty(last).
	eq(t, ids(queue.SortQueue(rows, "languages", true, nil)), []int{3, 2, 4, 1})
}

func TestSortQueue_EpisodeAirDate(t *testing.T) {
	t.Parallel()
	a := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	b := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	rows := []queue.Queue{
		{ID: 1, Episodes: []episodes.Episode{{AirDateUtc: &b}}},
		{ID: 2, Episodes: nil}, // no episode → nil air date → last asc
		{ID: 3, Episodes: []episodes.Episode{{AirDateUtc: &a}}},
	}
	eq(t, ids(queue.SortQueue(rows, "episode.airDateUtc", true, nil)), []int{3, 1, 2})
}

func TestSortQueue_DefaultKeyFallsBackToTimeLeft(t *testing.T) {
	t.Parallel()
	d1 := 1 * time.Hour
	d2 := 2 * time.Hour
	rows := []queue.Queue{
		{ID: 1, TimeLeft: &d2},
		{ID: 2, TimeLeft: &d1},
	}
	// Unknown key → timeleft default.
	eq(t, ids(queue.SortQueue(rows, "bogusKey", true, nil)), []int{2, 1})
}

func TestSortQueue_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	rows := []queue.Queue{{ID: 1, Size: 300}, {ID: 2, Size: 100}}
	_ = queue.SortQueue(rows, "size", true, nil)
	eq(t, ids(rows), []int{1, 2}) // original order preserved
}
