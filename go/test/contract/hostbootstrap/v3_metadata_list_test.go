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
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3MetadataListContract pins /api/v3/metadata to the
// C#-recorded golden — 4 seeded metadata providers (Xbmc/Wdtv/
// Roksbox/Plex inserted in that DI-scan order so the IDs match
// 1/2/3/4) sorted by Name ASC (Kodi/Plex/Roksbox/WDTV).
//
// Strategy: stand up a real SQLite via datastoretest.OpenSQLite,
// run metadata.Repo.SeedDefaults, then serve the list handler.
// Each row's Settings is the IProviderConfig constructor default,
// which equals the schema template's per-FieldDef Value — so the
// emitted fields[] is byte-identical to the schema endpoint's.
func TestV3MetadataListContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.metadata.list"

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
	db := datastoretest.OpenSQLite(t)
	repo, err := metadata.NewRepo(db)
	if err != nil {
		t.Fatalf("metadata.NewRepo: %v", err)
	}
	if err := repo.SeedDefaults(t.Context()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}

	const testKey = "v3-metadata-list-test-key"
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: entry.Request.Path,
			Handler: apiv3.MetadataListHandler(repo),
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
