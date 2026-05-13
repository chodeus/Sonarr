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
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualitydefinitions"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3QualityDefinitionContract pins the Go /api/v3/qualitydefinition
// output to the C#-recorded golden. The recording was taken against a
// freshly seeded DB (EnsureDefaults inserts the 22 default
// definitions in order). The test substitutes a Lister built directly
// from quality.DefaultDefinitions so the contract gate doesn't need a
// real SQLite — the auto-increment Ids the seed pass would have
// assigned are 1..22 in default-definition order, which is also the
// Weight-ASC order qualitydefinitions.Service.All sorts by.
func TestV3QualityDefinitionContract(t *testing.T) {
	const entryID = "v3.qualitydefinition"

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

	const testKey = "v3-qualitydefinition-test-key"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: "/api/v3/qualitydefinition",
			Handler: apiv3.QualityDefinitionHandler(defaultsLister{}),
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

// defaultsLister returns the 22 Decorated definitions a freshly
// seeded DB would expose. The seed pass inserts in
// quality.DefaultDefinitions order, assigning auto-increment Ids
// 1..22; that order also happens to be Weight-ASC stable, which is
// what qualitydefinitions.Service.All sorts by — so this fixture
// produces the same list a real *Service.All would on first read.
type defaultsLister struct{}

func (defaultsLister) All(context.Context) ([]qualitydefinitions.Decorated, error) {
	defs := quality.DefaultDefinitions()
	out := make([]qualitydefinitions.Decorated, len(defs))
	for i, d := range defs {
		out[i] = qualitydefinitions.Decorated{
			ID:        i + 1,
			Quality:   d.Quality,
			Title:     d.Title,
			Weight:    d.Weight,
			GroupName: d.GroupName,
		}
	}
	return out, nil
}
