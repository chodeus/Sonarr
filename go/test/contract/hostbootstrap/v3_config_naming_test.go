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
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3ConfigNamingContract pins the Go /api/v3/config/naming output
// to the C#-recorded golden. The recording was taken against a fresh
// DB where namingconfig.Default() is the seed; the fixture replicates
// that exactly (ID=1) so the byte-eq gate runs without a real DB.
func TestV3ConfigNamingContract(t *testing.T) {
	const entryID = "v3.config.naming"

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

	const testKey = "v3-config-naming-test-key"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: "/api/v3/config/naming",
			Handler: apiv3.NamingConfigHandler(defaultsNamingGetter{}),
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

// defaultsNamingGetter returns the canonical default NamingConfig
// (ID=1) — same row the seed migration inserts on first boot.
type defaultsNamingGetter struct{}

func (defaultsNamingGetter) GetConfig(context.Context) (*namingconfig.Config, error) {
	cfg := namingconfig.Default()
	cfg.ID = 1
	return cfg, nil
}
