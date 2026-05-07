package trackeddownloads_test

import (
	"strings"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// --- enum String() coverage ---------------------------------------

func TestStateStringMatchesCSharpNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   trackeddownloads.State
		want string
	}{
		{trackeddownloads.StateDownloading, "Downloading"},
		{trackeddownloads.StateImportBlocked, "ImportBlocked"},
		{trackeddownloads.StateImportPending, "ImportPending"},
		{trackeddownloads.StateImporting, "Importing"},
		{trackeddownloads.StateImported, "Imported"},
		{trackeddownloads.StateFailedPending, "FailedPending"},
		{trackeddownloads.StateFailed, "Failed"},
		{trackeddownloads.StateIgnored, "Ignored"},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("State(%d).String() = %q, want %q", int(tc.in), got, tc.want)
		}
	}
	if got := trackeddownloads.State(99).String(); got != "State(99)" {
		t.Errorf("out-of-range State.String = %q", got)
	}
}

func TestStatusStringMatchesCSharpNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   trackeddownloads.Status
		want string
	}{
		{trackeddownloads.StatusOK, "Ok"},
		{trackeddownloads.StatusWarning, "Warning"},
		{trackeddownloads.StatusError, "Error"},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("Status(%d).String() = %q, want %q", int(tc.in), got, tc.want)
		}
	}
	if got := trackeddownloads.Status(42).String(); got != "Status(42)" {
		t.Errorf("out-of-range Status.String = %q", got)
	}
}

// --- Track / Find / StopTracking happy paths ----------------------

func TestTrackInsertsRow(t *testing.T) {
	t.Parallel()
	s := trackeddownloads.NewService(nil)
	td := &trackeddownloads.TrackedDownload{DownloadID: "abc"}
	if err := s.Track(td); err != nil {
		t.Fatalf("Track: %v", err)
	}
	got := s.Find("abc")
	if got != td {
		t.Errorf("Find = %p, want stored ptr %p", got, td)
	}
}

func TestTrackReplacesOnDuplicateID(t *testing.T) {
	t.Parallel()
	s := trackeddownloads.NewService(nil)
	first := &trackeddownloads.TrackedDownload{DownloadID: "abc", Indexer: "first"}
	second := &trackeddownloads.TrackedDownload{DownloadID: "abc", Indexer: "second"}
	_ = s.Track(first)
	_ = s.Track(second)
	if got := s.Find("abc"); got == nil || got.Indexer != "second" {
		t.Errorf("got %+v, want second", got)
	}
}

func TestTrackRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	s := trackeddownloads.NewService(nil)

	if err := s.Track(nil); err == nil || !strings.Contains(err.Error(), "nil TrackedDownload") {
		t.Errorf("nil td: got %v, want nil-td error", err)
	}
	if err := s.Track(&trackeddownloads.TrackedDownload{DownloadID: ""}); err == nil ||
		!strings.Contains(err.Error(), "empty DownloadID") {
		t.Errorf("empty DownloadID: got %v", err)
	}

	var nilSvc *trackeddownloads.Service
	if err := nilSvc.Track(&trackeddownloads.TrackedDownload{DownloadID: "abc"}); err == nil ||
		!strings.Contains(err.Error(), "nil receiver") {
		t.Errorf("nil receiver: got %v", err)
	}
}

func TestFindReturnsNilForUnknown(t *testing.T) {
	t.Parallel()
	s := trackeddownloads.NewService(nil)
	if got := s.Find("missing"); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
	if got := s.Find(""); got != nil {
		t.Errorf("empty id: got %+v, want nil", got)
	}

	var nilSvc *trackeddownloads.Service
	if got := nilSvc.Find("abc"); got != nil {
		t.Errorf("nil receiver: got %+v, want nil", got)
	}
}

func TestStopTrackingRemovesAndReportsTrue(t *testing.T) {
	t.Parallel()
	s := trackeddownloads.NewService(nil)
	_ = s.Track(&trackeddownloads.TrackedDownload{DownloadID: "abc"})

	if !s.StopTracking("abc") {
		t.Errorf("StopTracking returned false on a present row")
	}
	if got := s.Find("abc"); got != nil {
		t.Errorf("row still present after StopTracking: %+v", got)
	}
	if s.StopTracking("abc") {
		t.Errorf("StopTracking returned true on a now-empty row (idempotent contract)")
	}
}

func TestStopTrackingNoOpsForBadInputs(t *testing.T) {
	t.Parallel()
	s := trackeddownloads.NewService(nil)
	if s.StopTracking("") {
		t.Errorf("empty id: should be false")
	}
	if s.StopTracking("missing") {
		t.Errorf("unknown id: should be false")
	}

	var nilSvc *trackeddownloads.Service
	if nilSvc.StopTracking("abc") {
		t.Errorf("nil receiver: should be false")
	}
}

func TestStopTrackingManyRemovesPresentSkipsAbsent(t *testing.T) {
	t.Parallel()
	s := trackeddownloads.NewService(nil)
	_ = s.Track(&trackeddownloads.TrackedDownload{DownloadID: "a"})
	_ = s.Track(&trackeddownloads.TrackedDownload{DownloadID: "b"})

	got := s.StopTrackingMany([]string{"a", "missing", "", "b"})
	if got != 2 {
		t.Errorf("removed = %d, want 2", got)
	}
	if s.Find("a") != nil || s.Find("b") != nil {
		t.Errorf("rows still present after StopTrackingMany")
	}
}

func TestStopTrackingManyEmptyInput(t *testing.T) {
	t.Parallel()
	s := trackeddownloads.NewService(nil)
	if got := s.StopTrackingMany(nil); got != 0 {
		t.Errorf("nil ids: got %d, want 0", got)
	}
	if got := s.StopTrackingMany([]string{}); got != 0 {
		t.Errorf("empty ids: got %d, want 0", got)
	}
}

func TestGetTrackedDownloadsReturnsSnapshot(t *testing.T) {
	t.Parallel()
	s := trackeddownloads.NewService(nil)
	_ = s.Track(&trackeddownloads.TrackedDownload{DownloadID: "a"})
	_ = s.Track(&trackeddownloads.TrackedDownload{DownloadID: "b"})
	_ = s.Track(&trackeddownloads.TrackedDownload{DownloadID: "c"})

	all := s.GetTrackedDownloads()
	if len(all) != 3 {
		t.Errorf("got %d, want 3", len(all))
	}

	// Snapshot independence: removing a row from the cache should
	// not affect the previously-returned slice.
	_ = s.StopTracking("a")
	if len(all) != 3 {
		t.Errorf("snapshot mutated: got %d, want 3", len(all))
	}
	if next := s.GetTrackedDownloads(); len(next) != 2 {
		t.Errorf("post-stop cache: got %d, want 2", len(next))
	}
}

// --- IndexerFlags + FindIndexerFlags ------------------------------

func TestIndexerFlagsNilSafe(t *testing.T) {
	t.Parallel()
	var td *trackeddownloads.TrackedDownload
	if got := td.IndexerFlags(); got != 0 {
		t.Errorf("nil TrackedDownload: got %v, want 0", got)
	}
	td = &trackeddownloads.TrackedDownload{}
	if got := td.IndexerFlags(); got != 0 {
		t.Errorf("nil RemoteEpisode: got %v, want 0", got)
	}
	td.RemoteEpisode = &decisionengine.Subject{}
	if got := td.IndexerFlags(); got != 0 {
		t.Errorf("nil Release: got %v, want 0", got)
	}
}

func TestIndexerFlagsReleaseInfo(t *testing.T) {
	t.Parallel()
	td := &trackeddownloads.TrackedDownload{
		RemoteEpisode: &decisionengine.Subject{
			Release: &parser.ReleaseInfo{IndexerFlags: parser.IndexerFlags(7)},
		},
	}
	if got := td.IndexerFlags(); got != parser.IndexerFlags(7) {
		t.Errorf("got %v, want 7", got)
	}
}

func TestIndexerFlagsTorrentInfo(t *testing.T) {
	t.Parallel()
	td := &trackeddownloads.TrackedDownload{
		RemoteEpisode: &decisionengine.Subject{
			Release: &parser.TorrentInfo{
				ReleaseInfo: parser.ReleaseInfo{IndexerFlags: parser.IndexerFlags(15)},
			},
		},
	}
	if got := td.IndexerFlags(); got != parser.IndexerFlags(15) {
		t.Errorf("got %v, want 15", got)
	}
}

func TestFindIndexerFlagsHit(t *testing.T) {
	t.Parallel()
	s := trackeddownloads.NewService(nil)
	td := &trackeddownloads.TrackedDownload{
		DownloadID: "abc",
		RemoteEpisode: &decisionengine.Subject{
			Release: &parser.ReleaseInfo{IndexerFlags: parser.IndexerFlags(42)},
		},
	}
	_ = s.Track(td)

	flags, ok := s.FindIndexerFlags("abc")
	if !ok || flags != parser.IndexerFlags(42) {
		t.Errorf("got (%v, %v), want (42, true)", flags, ok)
	}
}

func TestFindIndexerFlagsMissReturnsFalse(t *testing.T) {
	t.Parallel()
	s := trackeddownloads.NewService(nil)
	flags, ok := s.FindIndexerFlags("missing")
	if ok || flags != 0 {
		t.Errorf("got (%v, %v), want (0, false)", flags, ok)
	}
}

func TestFindIndexerFlagsHitWithZeroFlagsReturnsTrue(t *testing.T) {
	t.Parallel()
	// A row exists but with zero IndexerFlags — the orchestrator's
	// `if flags, ok := ...; ok { le.IndexerFlags = flags }` arm
	// should still write zero, matching C# semantics where the
	// field is always set when a tracked download exists.
	s := trackeddownloads.NewService(nil)
	_ = s.Track(&trackeddownloads.TrackedDownload{DownloadID: "abc"})

	flags, ok := s.FindIndexerFlags("abc")
	if !ok || flags != 0 {
		t.Errorf("got (%v, %v), want (0, true) — row exists with zero flags", flags, ok)
	}
}

// --- goroutine-safety smoke test ---------------------------------

func TestServiceConcurrentReadWriteSmoke(t *testing.T) {
	t.Parallel()
	s := trackeddownloads.NewService(nil)

	// Pre-populate.
	for i := 0; i < 10; i++ {
		_ = s.Track(&trackeddownloads.TrackedDownload{DownloadID: itoaIDX(i)})
	}

	var wg sync.WaitGroup
	const n = 50
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_ = s.Track(&trackeddownloads.TrackedDownload{DownloadID: itoaIDX(i % 10)})
		}(i)
		go func(i int) {
			defer wg.Done()
			_ = s.Find(itoaIDX(i % 10))
			_ = s.GetTrackedDownloads()
		}(i)
	}
	wg.Wait()

	// Cache size should be exactly 10 (the original IDs).
	if got := s.GetTrackedDownloads(); len(got) != 10 {
		t.Errorf("post-concurrent: got %d rows, want 10", len(got))
	}
}

// itoaIDX is a small itoa helper for the goroutine-safety test.
// Avoid pulling strconv just for the loop.
func itoaIDX(i int) string {
	if i == 0 {
		return "id-0"
	}
	out := ""
	for i > 0 {
		out = string(rune('0'+(i%10))) + out
		i /= 10
	}
	return "id-" + out
}
