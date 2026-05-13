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
	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3QueueStatusContract pins the Go /api/v3/queue/status output
// to the C#-recorded golden — all-zero / all-false envelope on a
// fresh DB with empty in-process queue.
func TestV3QueueStatusContract(t *testing.T) {
	const entryID = "v3.queue.status"

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
		t.Fatalf("entry %q missing from corpus", entryID)
	}

	const testKey = "v3-queue-status-test-key"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: "/api/v3/queue/status",
			Handler: apiv3.QueueStatusHandler(emptyQueueLister{}, emptyPending{}),
		}))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	base, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test URL: %v", err)
	}
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
	if got.BodyIsJSON != want.BodyIsJSON {
		t.Errorf("body kind: got json=%t want json=%t", got.BodyIsJSON, want.BodyIsJSON)
	}
	if !bytes.Equal(spec.CompactJSON(got.BodyJSON), spec.CompactJSON(want.BodyJSON)) {
		t.Errorf("body json drift:\n  got=%s\n  want=%s", got.BodyJSON, want.BodyJSON)
	}
}

type emptyQueueLister struct{}

func (emptyQueueLister) GetQueue() []queue.Queue { return nil }

type emptyPending struct{}

func (emptyPending) PendingCount(context.Context) (int, error) { return 0, nil }
