package episodes_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodes"
)

// fakeAutoUnmonitor implements AutoUnmonitorMissingProvider.
type fakeAutoUnmonitor bool

func (f fakeAutoUnmonitor) AutoUnmonitorPreviouslyDownloadedEpisodes() bool { return bool(f) }

func TestFileDeleteReason_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		r    episodes.FileDeleteReason
		want string
	}{
		{episodes.FileDeleteMissingFromDisk, "MissingFromDisk"},
		{episodes.FileDeleteManual, "Manual"},
		{episodes.FileDeleteUpgrade, "Upgrade"},
		{episodes.FileDeleteNoLinkedEpisodes, "NoLinkedEpisodes"},
		{episodes.FileDeleteManualOverride, "ManualOverride"},
		{episodes.FileDeleteReason(99), "Unknown"},
	}
	for _, c := range cases {
		if got := c.r.String(); got != c.want {
			t.Errorf("%d: got %q want %q", c.r, got, c.want)
		}
	}
}

func TestFileDeleteReason_FrozenValuesMatchEpisodeFiles(t *testing.T) {
	t.Parallel()
	// Numbering must mirror episodefiles.DeleteMediaFileReason exactly.
	// If either side reorders, the host's bus adapter mapping breaks.
	pins := map[episodes.FileDeleteReason]int{
		episodes.FileDeleteMissingFromDisk:  0,
		episodes.FileDeleteManual:           1,
		episodes.FileDeleteUpgrade:          2,
		episodes.FileDeleteNoLinkedEpisodes: 3,
		episodes.FileDeleteManualOverride:   4,
	}
	for r, want := range pins {
		if int(r) != want {
			t.Errorf("FileDeleteReason %s: got %d, want %d", r, int(r), want)
		}
	}
}

func TestHandleEpisodeFileAdded_BindsFileAndForgetsMissing(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, r := newServiceForTest(t, db)

	// Two unlinked episodes for series 1.
	a := &episodes.Episode{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1}
	b := &episodes.Episode{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 2}
	if _, err := r.Insert(t.Context(), a); err != nil {
		t.Fatalf("Insert a: %v", err)
	}
	if _, err := r.Insert(t.Context(), b); err != nil {
		t.Fatalf("Insert b: %v", err)
	}

	// Pre-seed missing cache; HandleAdded should clear A from it.
	svc.RememberMissingFromDisk(1, a.ID)
	svc.RememberMissingFromDisk(1, b.ID)

	const fileID = 99
	if err := svc.HandleEpisodeFileAdded(t.Context(), fileID, []episodes.Episode{*a, *b}); err != nil {
		t.Fatalf("HandleEpisodeFileAdded: %v", err)
	}

	// Both episodes now bound to fileID.
	for _, want := range []*episodes.Episode{a, b} {
		got, err := r.Get(t.Context(), want.ID)
		if err != nil {
			t.Fatalf("Get %d: %v", want.ID, err)
		}
		if got.EpisodeFileID == nil || *got.EpisodeFileID != fileID {
			t.Errorf("Get %d: EpisodeFileID=%v, want %d", want.ID, got.EpisodeFileID, fileID)
		}
	}

	// Cache cleared (both episodes forgotten).
	if drain := svc.DrainMissingFromDisk(1); drain != nil {
		t.Errorf("DrainMissingFromDisk(1) after Add: got %v, want nil", drain)
	}

	// Empty input is a no-op.
	if err := svc.HandleEpisodeFileAdded(t.Context(), 1, nil); err != nil {
		t.Errorf("HandleEpisodeFileAdded(nil): %v", err)
	}
}

func TestHandleEpisodeFileDeleted_PolicyMatrix(t *testing.T) {
	t.Parallel()

	type pol struct {
		reason        episodes.FileDeleteReason
		autoUnmonitor bool
		wantMonitored bool // expected post-delete Monitored flag
		wantInCache   bool // whether the episode should land in missing-from-disk cache
	}
	cases := map[string]pol{
		"upgrade autoUnmon=true": {
			reason: episodes.FileDeleteUpgrade, autoUnmonitor: true,
			wantMonitored: true, wantInCache: false,
		},
		"manualOverride autoUnmon=true": {
			reason: episodes.FileDeleteManualOverride, autoUnmonitor: true,
			wantMonitored: true, wantInCache: false,
		},
		"missing autoUnmon=true caches": {
			reason: episodes.FileDeleteMissingFromDisk, autoUnmonitor: true,
			wantMonitored: true, wantInCache: true,
		},
		"missing autoUnmon=false no cache no flip": {
			reason: episodes.FileDeleteMissingFromDisk, autoUnmonitor: false,
			wantMonitored: true, wantInCache: false,
		},
		"manual autoUnmon=true unmonitors immediately": {
			reason: episodes.FileDeleteManual, autoUnmonitor: true,
			wantMonitored: false, wantInCache: false,
		},
		"manual autoUnmon=false preserves Monitored": {
			reason: episodes.FileDeleteManual, autoUnmonitor: false,
			wantMonitored: true, wantInCache: false,
		},
		"noLinked autoUnmon=true unmonitors immediately": {
			reason: episodes.FileDeleteNoLinkedEpisodes, autoUnmonitor: true,
			wantMonitored: false, wantInCache: false,
		},
	}

	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			db := datastoretest.OpenSQLite(t)
			svc, r := newServiceForTest(t, db)
			svc.SetAutoUnmonitorMissingProvider(fakeAutoUnmonitor(p.autoUnmonitor))

			ep := &episodes.Episode{
				SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1,
				Monitored: true, EpisodeFileID: ptrInt(50),
			}
			if _, err := r.Insert(t.Context(), ep); err != nil {
				t.Fatalf("Insert: %v", err)
			}

			if err := svc.HandleEpisodeFileDeleted(t.Context(), 50, []episodes.Episode{*ep}, p.reason); err != nil {
				t.Fatalf("HandleEpisodeFileDeleted: %v", err)
			}

			got, err := r.Get(t.Context(), ep.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.EpisodeFileID != nil {
				t.Errorf("EpisodeFileID after delete: %v, want nil", got.EpisodeFileID)
			}
			if got.Monitored != p.wantMonitored {
				t.Errorf("Monitored: got %v, want %v", got.Monitored, p.wantMonitored)
			}

			drained := svc.DrainMissingFromDisk(1)
			if p.wantInCache {
				if len(drained) != 1 || drained[0] != ep.ID {
					t.Errorf("cache: got %v, want [%d]", drained, ep.ID)
				}
			} else if drained != nil {
				t.Errorf("cache: got %v, want nil", drained)
			}
		})
	}
}

func TestHandleEpisodeFileDeleted_TolerantOfMissingRows(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, _ := newServiceForTest(t, db)

	// No episodes inserted — ClearFileID returns ErrNotFound, which
	// the handler must swallow.
	ghost := episodes.Episode{ID: 999, SeriesID: 1}
	if err := svc.HandleEpisodeFileDeleted(t.Context(), 1, []episodes.Episode{ghost}, episodes.FileDeleteManual); err != nil {
		t.Errorf("HandleEpisodeFileDeleted(missing): %v", err)
	}
}

func TestHandleSeriesScanned_DrainsAndUnmonitors(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, r := newServiceForTest(t, db)

	// 2 monitored episodes; cache one of them as "missing".
	a := &episodes.Episode{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1, Monitored: true}
	b := &episodes.Episode{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 2, Monitored: true}
	if _, err := r.Insert(t.Context(), a); err != nil {
		t.Fatalf("Insert a: %v", err)
	}
	if _, err := r.Insert(t.Context(), b); err != nil {
		t.Fatalf("Insert b: %v", err)
	}
	svc.RememberMissingFromDisk(1, a.ID)

	if err := svc.HandleSeriesScanned(t.Context(), 1); err != nil {
		t.Fatalf("HandleSeriesScanned: %v", err)
	}

	// A is unmonitored, B unchanged.
	gotA, _ := r.Get(t.Context(), a.ID)
	gotB, _ := r.Get(t.Context(), b.ID)
	if gotA.Monitored {
		t.Errorf("A: Monitored=true, want false")
	}
	if !gotB.Monitored {
		t.Errorf("B: Monitored=false, want true")
	}
	// Cache drained.
	if d := svc.DrainMissingFromDisk(1); d != nil {
		t.Errorf("cache after scan: %v, want nil", d)
	}

	// Idempotent: empty cache → no-op.
	if err := svc.HandleSeriesScanned(t.Context(), 1); err != nil {
		t.Errorf("HandleSeriesScanned (empty): %v", err)
	}
}
