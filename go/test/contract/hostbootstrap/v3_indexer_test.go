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
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3IndexerContract pins /api/v3/indexer to the C#-recorded
// golden — empty array on a fresh DB.
func TestV3IndexerContract(t *testing.T) {
	const entryID = "v3.indexer.list"

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

	const testKey = "v3-indexer-test-key"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: "/api/v3/indexer",
			Handler: apiv3.IndexerHandler(emptyIndexerLister{}),
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

type emptyIndexerLister struct{}

func (emptyIndexerLister) All(context.Context) ([]indexers.Indexer, error) { return nil, nil }
