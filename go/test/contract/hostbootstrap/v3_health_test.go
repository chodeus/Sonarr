package hostbootstrap

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3HealthContract pins /api/v3/health to the C#-recorded golden
// — three failing-check entries on a fresh DB: IndexerRssCheck
// (Error), DownloadClientCheck (Warning), IndexerSearchCheck
// (Warning).
//
// Strategy: stand up a minimal healthcheck.Service with only the
// three new checks fed by zero-count fixtures (no indexers, no
// download clients — matching the recording host's empty DB). Run
// HandleApplicationStarted, then serve the handler against the live
// Service. The handler's deterministic (Type DESC, Source ASC) sort
// produces the recording's exact emit order.
//
// The minimal service intentionally excludes the other 12 production
// checks. They MAY produce results on a fresh CI machine (SkyHook
// network probe, SystemTime NTP check, etc.) so isolating to just
// the three new checks keeps the byte-eq deterministic. Production
// drift on the other checks is covered by their dedicated unit
// tests; full-stack integration validation is tracked as a follow-up
// in STATUS.
func TestV3HealthContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.system.health"

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("find repo root: %v", err)
	}
	corpusPath := filepath.Join(repoRoot, "test", "contract", "corpus", "v3-empty-state.json")
	goldenPath := filepath.Join(repoRoot, "test", "contract", "golden", "v3-empty-state", entryID+".golden.json")

	corpus, err := spec.LoadCorpus(corpusPath)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	var entry spec.Entry
	for _, e := range corpus.Entries {
		if e.ID == entryID {
			entry = e
			break
		}
	}
	if entry.ID == "" {
		t.Fatalf("entry %q missing", entryID)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_ = t // bus shutdown handled via the bus's own Wait if needed; for the contract test the SubscribeAllAsync handler is fire-and-forget.
	bus := messaging.New(logger)

	rssCheck, err := healthcheck.NewIndexerRssCheck(zeroRssLister{})
	if err != nil {
		t.Fatalf("NewIndexerRssCheck: %v", err)
	}
	searchCheck, err := healthcheck.NewIndexerSearchCheck(zeroSearchLister{})
	if err != nil {
		t.Fatalf("NewIndexerSearchCheck: %v", err)
	}
	dcCheck, err := healthcheck.NewDownloadClientCheck(zeroDownloadClientLister{})
	if err != nil {
		t.Fatalf("NewDownloadClientCheck: %v", err)
	}

	hcSvc, err := healthcheck.NewService([]healthcheck.Check{rssCheck, searchCheck, dcCheck}, bus, logger)
	if err != nil {
		t.Fatalf("healthcheck.NewService: %v", err)
	}
	if err := hcSvc.HandleApplicationStarted(t.Context()); err != nil {
		t.Fatalf("HandleApplicationStarted: %v", err)
	}

	const testKey = "v3-health-test-key"
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: entry.Request.Path,
			Handler: apiv3.HealthHandler(hcSvc),
		}))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	base, _ := url.Parse(ts.URL)
	client := &runner.Client{Base: base, APIKey: testKey}
	got, err := client.Run(context.Background(), entry)
	if err != nil {
		t.Fatalf("client.Run: %v", err)
	}
	want, err := spec.LoadGolden(goldenPath)
	if err != nil {
		t.Fatalf("load golden: %v", err)
	}
	if got.Status != want.Status {
		t.Errorf("status: got %d want %d", got.Status, want.Status)
	}
	if !bytes.Equal(spec.CompactJSON(got.BodyJSON), spec.CompactJSON(want.BodyJSON)) {
		t.Errorf("body drift:\n got=%s\n want=%s", got.BodyJSON, want.BodyJSON)
	}
}

type zeroRssLister struct{}

func (zeroRssLister) RssEnabledCount(context.Context, bool) (int, error) { return 0, nil }

type zeroSearchLister struct{}

func (zeroSearchLister) AutomaticSearchEnabledCount(context.Context, bool) (int, error) {
	return 0, nil
}
func (zeroSearchLister) InteractiveSearchEnabledCount(context.Context) (int, error) { return 0, nil }

type zeroDownloadClientLister struct{}

func (zeroDownloadClientLister) EnabledDownloadClientCount(context.Context) (int, error) {
	return 0, nil
}
