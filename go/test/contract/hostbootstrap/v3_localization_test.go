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
	"sonarr.tv/sonarr/internal/localization"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3LocalizationContract pins /api/v3/localization to the
// C#-recorded golden — `{"strings": {<2269 entries>}}`. The Go
// implementation embeds the same en.json source file used by the C#
// build (`src/NzbDrone.Core/Localization/Core/en.json`), so the
// dictionary content matches verbatim. The contract-test normalizer
// alphabetizes object keys before diff, so map-iteration order is
// unobservable on byte-eq.
//
// If this test starts failing after a C# tree update, the source
// file changed and the Go-side copy under `internal/localization/
// en.json` needs to be refreshed.
func TestV3LocalizationContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.localization"

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

	const testKey = "v3-localization-test-key"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := localization.NewService()
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: entry.Request.Path,
			Handler: apiv3.LocalizationHandler(svc),
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
		t.Errorf("body drift (length got=%d want=%d)", len(got.BodyJSON), len(want.BodyJSON))
	}
}
