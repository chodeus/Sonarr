package main

import (
	"testing"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/series"
)

func ptr(i int) *int { return &i }

// J422 (G3): queueDetailsAdapter returns the queue filtered by seriesId
// or episodeIds (seriesId taking precedence), else the full queue.
func TestQueueDetailsAdapter_Filtering_J422(t *testing.T) {
	t.Parallel()
	svc := queue.NewService()
	svc.SetQueue([]queue.Queue{
		{ID: 1, Series: &series.Series{ID: 10}, Episodes: []episodes.Episode{{ID: 100}, {ID: 101}}},
		{ID: 2, Series: &series.Series{ID: 20}, Episodes: []episodes.Episode{{ID: 200}}},
		{ID: 3, Series: &series.Series{ID: 10}, Episodes: []episodes.Episode{{ID: 102}}},
	})
	a := queueDetailsAdapter{svc: svc}
	ctx := t.Context()

	// No filter → all 3.
	all, err := a.QueueDetails(ctx, nil, nil)
	if err != nil || len(all) != 3 {
		t.Fatalf("no filter: got %d rows (err %v), want 3", len(all), err)
	}

	// seriesId=10 → rows 1 + 3.
	bySeries, _ := a.QueueDetails(ctx, ptr(10), nil)
	if len(bySeries) != 2 {
		t.Errorf("seriesId=10: got %d, want 2", len(bySeries))
	}

	// episodeIds=[200] → row 2 only.
	byEp, _ := a.QueueDetails(ctx, nil, []int{200})
	if len(byEp) != 1 || byEp[0].ID != 2 {
		t.Errorf("episodeIds=[200]: got %v, want [row 2]", byEp)
	}

	// episodeIds=[100,102] → rows 1 + 3.
	byEps, _ := a.QueueDetails(ctx, nil, []int{100, 102})
	if len(byEps) != 2 {
		t.Errorf("episodeIds=[100,102]: got %d, want 2", len(byEps))
	}

	// seriesId takes precedence over episodeIds (C# order).
	prec, _ := a.QueueDetails(ctx, ptr(20), []int{100})
	if len(prec) != 1 || prec[0].ID != 2 {
		t.Errorf("seriesId precedence: got %v, want [row 2]", prec)
	}
}

func TestQueueRowMatchesEpisodes_J422(t *testing.T) {
	t.Parallel()
	q := queue.Queue{Episodes: []episodes.Episode{{ID: 5}, {ID: 6}}}
	if !queueRowMatchesEpisodes(q, map[int]bool{6: true}) {
		t.Error("should match on episode 6")
	}
	if queueRowMatchesEpisodes(q, map[int]bool{9: true}) {
		t.Error("should not match episode 9")
	}
}
