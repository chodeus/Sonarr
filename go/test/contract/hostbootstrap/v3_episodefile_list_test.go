package hostbootstrap

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3EpisodeFileListContract_404OnMissingSeries pins the Go
// /api/v3/episodefile?seriesId=N status code to 404 when no series
// with that id is persisted — mirrors the recorded C# golden's
// `"status": 404` and closes the contract-replay 404-vs-200 drift
// (slice 5.J180). The C# behaviour comes from
// `EpisodeFileController.cs:72` `_seriesService.GetSeries(...)`
// throwing `ModelNotFoundException` which the global exception
// filter surfaces as 404 + a stack-trace body. The Go-port emits a
// shorter "Series with ID N does not exist" body — we don't compare
// body bytes because the C# golden's body is a full .NET stack
// trace that's not parity-replicable; the status code is the wire
// drift that actually affects callers.
func TestV3EpisodeFileListContract_404OnMissingSeries(t *testing.T) {
	const entryID = "v3.episodefile.list"

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

	const testKey = "v3-episodefile-list-test-key"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := emptyEpisodeFileReader{}
	checker := absentSeriesChecker{}
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: "/api/v3/episodefile",
			Handler: apiv3.EpisodeFileListHandler(svc, checker),
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
		t.Errorf("status: got %d want %d (404-vs-200 drift unresolved?)", got.Status, want.Status)
	}
}

// emptyEpisodeFileReader satisfies the EpisodeFileReader surface
// the handler requires. Returns nil/empty everywhere — the
// 404-on-missing-series path short-circuits before any of these
// methods get called.
type emptyEpisodeFileReader struct{}

func (emptyEpisodeFileReader) GetFile(context.Context, int) (*episodefiles.File, error) {
	return nil, episodefiles.ErrNotFound
}

func (emptyEpisodeFileReader) GetFiles(context.Context, []int) ([]episodefiles.File, error) {
	return nil, nil
}

func (emptyEpisodeFileReader) GetFilesBySeries(context.Context, int) ([]episodefiles.File, error) {
	return nil, nil
}

// absentSeriesChecker reports every id as missing — the production
// recording was taken against a freshly-seeded empty-state DB, so
// seriesId=1 (the corpus entry's query) hits 404 by construction.
type absentSeriesChecker struct{}

func (absentSeriesChecker) SeriesExists(context.Context, int) (bool, error) {
	return false, nil
}
