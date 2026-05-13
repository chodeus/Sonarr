package hostbootstrap

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3TagDetailContract pins /api/v3/tag/detail to the C#-recorded
// golden — an empty list on a fresh DB. Populated-row join across
// every tag-consumer table is DEFERRED until the consumer adapters
// land; the empty-DB recording carries zero entries so the gap is
// unobservable today.
func TestV3TagDetailContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.tag.detail"
	checkEmptyJSONContract(t, entryID, apiv3.TagDetailHandler(emptyTagDetailLister{}))
}

type emptyTagDetailLister struct{}

func (emptyTagDetailLister) AllTagDetails(context.Context) ([]apiv3.TagDetailsResource, error) {
	return nil, nil
}

// checkEmptyJSONContract is a small helper shared by the empty-
// list contract tests in this slice (tag/detail + update). The
// other contract tests handle their own bootstrapping because they
// either need request-specific scope (wanted) or a real DB
// (qualityprofile) — those don't share enough boilerplate to
// generalise.
func checkEmptyJSONContract(t *testing.T, entryID string, handler http.Handler) {
	t.Helper()
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

	const testKey = "v3-empty-contract-key"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: entry.Request.Path, Handler: handler,
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
