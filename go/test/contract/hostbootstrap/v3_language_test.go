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
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3LanguageContract pins the Go /api/v3/language output to the
// C#-recorded golden. Unlike host_bootstrap_test.go, this test never
// regenerates — the golden was recorded against the C# binary in
// Phase 0.5 and is the byte-equality target. If the Go port drifts,
// the failure pinpoints which field diverged.
//
// Filter approach: load the full v3-empty-state corpus, run only the
// `v3.language` entry. Future per-endpoint tests follow the same
// pattern, filtering to their own entry as the controller ports.
func TestV3LanguageContract(t *testing.T) {
	const entryID = "v3.language"

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

	const testKey = "v3-language-test-key"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	langs := language.All()
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: "/api/v3/language",
			Handler: apiv3.LanguageHandler(langs),
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
