package download_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/historyhandlers"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// stubHistoryWriter satisfies historyhandlers.EntryWriter. Captures
// every Add into a slice so the integration test can assert on the
// per-episode history rows the listener wrote.
type stubHistoryWriter struct {
	mu      sync.Mutex
	entries []*history.Entry
}

func (s *stubHistoryWriter) Add(_ context.Context, e *history.Entry) (*history.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return e, nil
}

// TestIntegration_OrchestratorFullPipeline — slice 6.A5: composes
// every 6.A* slice end-to-end. Wires the orchestrator with all four
// optional deps (bus, recorder, fetcher, prioritizer-omitted) and
// drives a single Usenet decision through. Asserts:
//
//   - 6.A4: body pre-fetch populated GrabRequest.NzbBody before
//     Provider.Download was called
//   - 6.A2: EpisodeGrabbedEvent fired on the bus with the full
//     download-client identity payload
//   - 6.A2 listener: historyhandlers.HandleEpisodeGrabbed (registered
//     in production by cmd/sonarr/main.go:822-825) ran and wrote one
//     history.Entry row per episode in the Subject
//   - 6.A3: providerstatus.Recorder.RecordSuccess fired with the
//     Definition.ID — escalation level decrements + DisabledTill
//     clears
//
// This is the single canonical proof that the four orchestrator
// slices compose. Per-slice unit tests already cover the corner
// cases; this one verifies they don't trip over each other when
// stacked.
func TestIntegration_OrchestratorFullPipeline(t *testing.T) {
	t.Parallel()

	// Two-episode subject — listener should write TWO history.Entry
	// rows from one EpisodeGrabbedEvent. Mirrors the C# foreach loop
	// inside HistoryService.Handle(EpisodeGrabbedEvent) at :144-194.
	sub := decisionengine.NewSubject()
	sub.Series = &series.Series{ID: 42, Title: "Integration Show"}
	sub.Episodes = []episodes.Episode{
		{ID: 101, SeriesID: 42},
		{ID: 102, SeriesID: 42},
	}
	sub.Release = &parser.ReleaseInfo{
		Title:            "Integration.Show.S01.NZB",
		DownloadURL:      "https://indexer.test/release.nzb",
		IndexerID:        7,
		Indexer:          "MyIndexer",
		DownloadProtocol: indexers.DownloadProtocolUsenet,
	}
	dec := decisionengine.Decision{Subject: sub}

	// Provider with a definition so RecordSuccess + the listener
	// payload both get a real DownloadClientID.
	def := &downloadclients.DownloadClient{ID: 31, Name: "MainSAB", Implementation: "Sabnzbd"}
	prov := &stubProvider{
		name:       "sab",
		protocol:   indexers.DownloadProtocolUsenet,
		downloadID: "nzo_integration",
		definition: def,
	}
	lister := &stubClientLister{usenet: []downloadclients.Provider{prov}}
	pdd, err := download.NewProcessDownloadDecisions(lister, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewProcessDownloadDecisions: %v", err)
	}

	// Wire all four optional deps.
	bus := messaging.New(slog.Default())
	rec := &stubRecorder{}
	writer := &stubHistoryWriter{}
	fetcher := &stubBodyFetcher{body: []byte("<?xml ?><nzb>integration</nzb>")}

	pdd.SetBus(bus)
	pdd.SetRecorder(rec)
	pdd.SetBodyFetcher(fetcher)

	// Subscribe the production listener — same shape as
	// cmd/sonarr/main.go:822-825.
	messaging.Subscribe(bus, "history.HandleEpisodeGrabbed",
		func(ctx context.Context, ev decisionengine.EpisodeGrabbedEvent) error {
			return historyhandlers.HandleEpisodeGrabbed(ctx, writer, &ev)
		})

	// Drive a single decision through.
	out := pdd.ProcessDecisions(context.Background(), []decisionengine.Decision{dec})

	if len(out.Grabbed) != 1 {
		t.Fatalf("Grabbed = %d, want 1; full result: %s", len(out.Grabbed), out)
	}
	if len(out.Pending)+len(out.Rejected) != 0 {
		t.Errorf("expected zero Pending/Rejected; got %s", out)
	}

	// --- Slice 6.A4 — body pre-fetched into GrabRequest -------------
	if fetcher.calls != 1 {
		t.Errorf("fetcher.calls = %d, want 1", fetcher.calls)
	}
	if fetcher.lastURL != "https://indexer.test/release.nzb" {
		t.Errorf("fetcher.lastURL = %q, want %q", fetcher.lastURL, "https://indexer.test/release.nzb")
	}
	if fetcher.lastIndexer != 7 {
		t.Errorf("fetcher.lastIndexer = %d, want 7", fetcher.lastIndexer)
	}
	if prov.lastReq == nil {
		t.Fatal("provider did not receive a GrabRequest")
	}
	if string(prov.lastReq.NzbBody) != "<?xml ?><nzb>integration</nzb>" {
		t.Errorf("Provider.NzbBody = %q, want pre-fetched payload", string(prov.lastReq.NzbBody))
	}

	// --- Slice 6.A3 — recorder captured RecordSuccess ----------------
	rec.mu.Lock()
	successIDs := append([]int(nil), rec.success...)
	failureIDs := append([]int(nil), rec.failure...)
	connFailIDs := append([]int(nil), rec.connectionFail...)
	rec.mu.Unlock()
	if len(successIDs) != 1 || successIDs[0] != 31 {
		t.Errorf("RecordSuccess providerIDs = %v, want [31]", successIDs)
	}
	if len(failureIDs)+len(connFailIDs) != 0 {
		t.Errorf("expected zero failure/connection records; got failure=%v connFail=%v", failureIDs, connFailIDs)
	}

	// --- Slice 6.A2 listener — historyhandlers wrote one row per episode -
	writer.mu.Lock()
	rows := append([]*history.Entry(nil), writer.entries...)
	writer.mu.Unlock()
	if len(rows) != 2 {
		t.Fatalf("history rows written = %d, want 2 (one per episode)", len(rows))
	}
	episodeIDs := map[int]bool{}
	for _, r := range rows {
		episodeIDs[r.EpisodeID] = true
		if r.DownloadID != "nzo_integration" {
			t.Errorf("row.DownloadID = %q, want %q", r.DownloadID, "nzo_integration")
		}
		if r.SeriesID != 42 {
			t.Errorf("row.SeriesID = %d, want 42", r.SeriesID)
		}
		if r.SourceTitle != "Integration.Show.S01.NZB" {
			t.Errorf("row.SourceTitle = %q, want %q", r.SourceTitle, "Integration.Show.S01.NZB")
		}
		if r.EventType == nil || *r.EventType != int(history.EventTypeGrabbed) {
			t.Errorf("row.EventType = %v, want EventTypeGrabbed=%d", r.EventType, history.EventTypeGrabbed)
		}
		if data, ok := r.Data["DownloadClient"]; !ok || data != "Sabnzbd" {
			t.Errorf("row.Data[DownloadClient] = %q, want %q (DownloadClient.Implementation propagated through EpisodeGrabbedEvent)", data, "Sabnzbd")
		}
		if data, ok := r.Data["DownloadClientName"]; !ok || data != "MainSAB" {
			t.Errorf("row.Data[DownloadClientName] = %q, want %q (DownloadClient.Name)", data, "MainSAB")
		}
	}
	if !episodeIDs[101] || !episodeIDs[102] {
		t.Errorf("episode IDs in rows = %v, want both 101 and 102", episodeIDs)
	}

	// --- Slice 6.A2 — Subject.DownloadAllowed was stamped -----------
	grabbedSub, _ := out.Grabbed[0].Subject.(*decisionengine.Subject)
	if grabbedSub == nil || !grabbedSub.DownloadAllowed {
		t.Error("Grabbed Subject.DownloadAllowed not flipped to true")
	}
}
